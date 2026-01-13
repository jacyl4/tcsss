package route

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	terr "tcsss/internal/errors"
)

// Optimizer handles route table optimization.
type Optimizer struct {
	logger               *slog.Logger
	cfg                  WindowConfig
	initCwndSegments     int
	initRwndSegments     int
	loopbackCwndSegments int
	loopbackRwndSegments int
	netlink              NetlinkClient
	executor             CommandExecutor
	commandTimeout       time.Duration
}

// NewOptimizer constructs an Optimizer with dependencies.
func NewOptimizer(logger *slog.Logger, cfg WindowConfig, deps Dependencies) *Optimizer {
	cfg = cfg.WithDefaults()

	initCwndSegments := bytesToSegments(cfg.InitCwndBytes, cfg.MSSBytes)
	initRwndSegments := bytesToSegments(cfg.InitRwndBytes, cfg.MSSBytes)
	loopbackWindowSegments := bytesToSegments(cfg.LoopbackWindowBytes, cfg.MSSBytes)

	opt := &Optimizer{
		logger:               logger,
		cfg:                  cfg,
		initCwndSegments:     initCwndSegments,
		initRwndSegments:     initRwndSegments,
		loopbackCwndSegments: loopbackWindowSegments,
		loopbackRwndSegments: loopbackWindowSegments,
		netlink:              deps.Netlink,
		executor:             deps.Executor,
		commandTimeout:       deps.CommandTimeout,
	}

	if opt.commandTimeout <= 0 {
		opt.commandTimeout = defaultCmdTimeout
	}

	if opt.logger != nil {
		opt.logger.Info("Route optimizer initialized",
			slog.Int("mss_bytes", opt.cfg.MSSBytes),
			slog.Int("initcwnd_bytes", opt.cfg.InitCwndBytes),
			slog.Int("initcwnd_segments", opt.initCwndSegments),
			slog.Int("initrwnd_bytes", opt.cfg.InitRwndBytes),
			slog.Int("initrwnd_segments", opt.initRwndSegments),
			slog.Int("loopback_window_bytes", opt.cfg.LoopbackWindowBytes),
			slog.Int("loopback_window_segments", opt.loopbackCwndSegments))
	}

	return opt
}

// Optimize applies route tuning for loopback, local and NIC routes.
func (opt *Optimizer) Optimize(ctx context.Context) error {
	var errs terr.MultiError

	if err := opt.optimizeLoopback(ctx); err != nil {
		errs.Add(fmt.Errorf("loopback: %w", err))
		if opt.logger != nil {
			opt.logger.Warn("Failed to optimize loopback routes", slog.String("error", err.Error()))
		}
	}

	if err := opt.optimizeLocal(ctx); err != nil {
		errs.Add(fmt.Errorf("local: %w", err))
		if opt.logger != nil {
			opt.logger.Warn("Failed to optimize local routes", slog.String("error", err.Error()))
		}
	}

	if err := opt.optimizeNIC(ctx); err != nil {
		errs.Add(fmt.Errorf("nic: %w", err))
		if opt.logger != nil {
			opt.logger.Warn("Failed to optimize NIC routes", slog.String("error", err.Error()))
		}
	}

	finalErr := errs.ErrorOrNil()
	if opt.logger != nil {
		if finalErr != nil {
			opt.logger.Warn("Route optimization completed with errors", slog.Int("error_count", errs.Len()))
		} else {
			opt.logger.Info("Route optimization completed successfully")
		}
	}

	return finalErr
}

func (opt *Optimizer) optimizeLocal(ctx context.Context) error {
	job := routeJob{
		category:       "local",
		routeArgs:      []string{"route", "show", "table", "local"},
		filter:         shouldOptimizeLocal,
		params:         newParams(1500, opt.initCwndSegments, opt.initRwndSegments, "cubic"),
		fetchOperation: "fetch_local_routes",
		applyOperation: "optimize_local_routes",
	}
	return opt.optimize(ctx, job)
}

func (opt *Optimizer) optimizeLoopback(ctx context.Context) error {
	job := routeJob{
		category:       "loopback",
		routeArgs:      []string{"route", "show", "table", "local"},
		filter:         shouldOptimizeLoopback,
		params:         newParams(65520, opt.loopbackCwndSegments, opt.loopbackRwndSegments, "cubic"),
		fetchOperation: "fetch_loopback_routes",
		applyOperation: "optimize_loopback_routes",
	}
	return opt.optimize(ctx, job)
}

func (opt *Optimizer) optimizeNIC(ctx context.Context) error {
	nic, err := opt.getPrimaryNIC()
	if err != nil || nic == "" {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("failed to detect primary NIC: %w", err),
			terr.ErrorContext{Operation: "detect_primary_nic"},
		)
	}

	congctl, err := opt.getCurrentCongestionControl()
	if err != nil {
		congctl = "cubic"
	}

	job := routeJob{
		category:  "nic",
		routeArgs: []string{"route", "show"},
		filter: func(line string) bool {
			return shouldOptimizeNIC(line, nic)
		},
		params:         newParams(1500, opt.initCwndSegments, opt.initRwndSegments, congctl),
		fetchOperation: "fetch_nic_routes",
		applyOperation: "optimize_nic_routes",
		commonLogAttrs: []slog.Attr{
			slog.String("interface", nic),
			slog.String("congctl", congctl),
		},
		commonErrContext: terr.ErrorContext{Interface: nic},
	}
	return opt.optimize(ctx, job)
}

func (opt *Optimizer) optimize(ctx context.Context, job routeJob) error {
	lines, err := opt.fetchRoutes(ctx, job.routeArgs...)
	if err != nil {
		return job.fetchError(err)
	}

	filtered := opt.filterRoutes(lines, job.filter)

	if opt.logger != nil {
		attrs := appendAttrs(job.commonLogAttrs,
			slog.Int("total_routes", len(filtered)),
		)
		opt.logger.Info(fmt.Sprintf("%s routes optimization started", job.category), terr.AttrsToArgs(attrs)...)
	}

	start := time.Now()
	optimized, skipped, applyErr := opt.applyRoutes(ctx, filtered, job.params.args(), job.category)

	if opt.logger != nil {
		attrs := appendAttrs(job.commonLogAttrs,
			slog.Int("optimized", optimized),
			slog.Int("skipped", skipped),
			slog.Int("total", len(filtered)),
			slog.Duration("duration", time.Since(start)),
		)
		opt.logger.Info(fmt.Sprintf("%s routes optimization completed", job.category), terr.AttrsToArgs(attrs)...)
	}

	if applyErr != nil {
		return job.applyError(applyErr)
	}
	return nil
}

func appendAttrs(base []slog.Attr, additional ...slog.Attr) []slog.Attr {
	if len(additional) == 0 {
		return cloneAttrs(base)
	}
	result := make([]slog.Attr, 0, len(base)+len(additional))
	result = append(result, base...)
	result = append(result, additional...)
	return result
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]slog.Attr, len(attrs))
	copy(out, attrs)
	return out
}

type routeFilter func(string) bool

type routeJob struct {
	category         string
	routeArgs        []string
	filter           routeFilter
	params           params
	fetchOperation   string
	applyOperation   string
	commonLogAttrs   []slog.Attr
	commonErrContext terr.ErrorContext
}

func (job routeJob) fetchError(err error) error {
	context := terr.ErrorContext{Operation: job.fetchOperation}.Merge(job.commonErrContext)
	return terr.New(
		terr.CategoryRecoverable,
		fmt.Errorf("fetch %s routes: %w", job.category, err),
		context,
	)
}

func (job routeJob) applyError(err error) error {
	context := terr.ErrorContext{Operation: job.applyOperation}.Merge(job.commonErrContext)
	return terr.New(
		terr.CategoryRecoverable,
		fmt.Errorf("apply %s route changes: %w", job.category, err),
		context,
	)
}

func (opt *Optimizer) cleanRouteLine(line string) string {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return ""
	}

	result := make([]string, 0, len(tokens))
	skipNext := false

	for i := 0; i < len(tokens); i++ {
		if skipNext {
			skipNext = false
			continue
		}

		token := tokens[i]
		switch token {
		case "mtu", "initcwnd", "initrwnd", "fastopen_no_cookie":
			if i+1 < len(tokens) {
				skipNext = true
			}
			continue
		case "congctl":
			if i+2 < len(tokens) && tokens[i+1] == "lock" {
				i += 2
				continue
			}
		}
		result = append(result, token)
	}

	return strings.Join(result, " ")
}

func (opt *Optimizer) applyRouteChange(ctx context.Context, routeLine string, params ...string) error {
	tokens := strings.Fields(routeLine)
	if len(tokens) == 0 {
		return fmt.Errorf("empty route line")
	}
	args := append([]string{"route", "change"}, tokens...)
	args = append(args, params...)
	if _, err := opt.runIPCommand(ctx, args...); err != nil {
		return fmt.Errorf("ip %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (opt *Optimizer) runIPCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := opt.commandContext(ctx)
	defer cancel()

	executor := ensureExecutor(opt.executor)
	return executor.Run(ctx, "ip", args)
}

func (opt *Optimizer) runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := opt.commandContext(ctx)
	defer cancel()

	executor := ensureExecutor(opt.executor)
	return executor.Run(ctx, name, args)
}

func (opt *Optimizer) fetchRoutes(ctx context.Context, args ...string) ([]string, error) {
	output, err := opt.runIPCommand(ctx, args...)
	if err != nil {
		return nil, err
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func (opt *Optimizer) commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := opt.commandTimeout
	if timeout <= 0 {
		timeout = defaultCmdTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func (opt *Optimizer) filterRoutes(lines []string, predicate routeFilter) []string {
	result := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if predicate(line) {
			result = append(result, line)
		}
	}
	return result
}

func (opt *Optimizer) applyRoutes(ctx context.Context, routes []string, params []string, category string) (int, int, error) {
	if len(routes) == 0 {
		return 0, 0, nil
	}

	optimized := 0
	failures := 0
	var firstErr error

	for _, route := range routes {
		if route == "" {
			continue
		}
		routeLine := opt.cleanRouteLine(route)
		if routeLine == "" {
			continue
		}
		if err := opt.applyRouteChange(ctx, routeLine, params...); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if opt.logger != nil {
				opt.logger.Debug("route optimization skipped",
					slog.String("category", category),
					slog.String("route", routeLine),
					slog.String("error", err.Error()))
			}
			failures++
			continue
		}
		optimized++
		if opt.logger != nil {
			opt.logger.Debug("route optimization applied",
				slog.String("category", category),
				slog.String("route", routeLine))
		}
	}

	return optimized, failures, firstErr
}

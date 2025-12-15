package route

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

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

type routeEntry struct {
	route    netlink.Route
	linkName string
	linkDown bool
}

type routeFilter func(routeEntry) bool

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
		if isContextError(err) {
			return err
		}
		errs.Add(fmt.Errorf("loopback: %w", err))
		if opt.logger != nil {
			opt.logger.Warn("Failed to optimize loopback routes", slog.String("error", err.Error()))
		}
	}

	if err := opt.optimizeLocal(ctx); err != nil {
		if isContextError(err) {
			return err
		}
		errs.Add(fmt.Errorf("local: %w", err))
		if opt.logger != nil {
			opt.logger.Warn("Failed to optimize local routes", slog.String("error", err.Error()))
		}
	}

	if err := opt.optimizeNIC(ctx); err != nil {
		if isContextError(err) {
			return err
		}
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
		category:         "local",
		table:            unix.RT_TABLE_LOCAL,
		filter:           shouldOptimizeLocalRoute,
		params:           newParams(1500, opt.initCwndSegments, opt.initRwndSegments, "cubic"),
		fetchOperation:   "fetch_local_routes",
		applyOperation:   "optimize_local_routes",
		commonLogAttrs:   nil,
		commonErrContext: terr.ErrorContext{},
	}
	return opt.optimize(ctx, job)
}

func (opt *Optimizer) optimizeLoopback(ctx context.Context) error {
	job := routeJob{
		category:         "loopback",
		table:            unix.RT_TABLE_LOCAL,
		filter:           shouldOptimizeLoopbackRoute,
		params:           newParams(65520, opt.loopbackCwndSegments, opt.loopbackRwndSegments, "cubic"),
		fetchOperation:   "fetch_loopback_routes",
		applyOperation:   "optimize_loopback_routes",
		commonLogAttrs:   nil,
		commonErrContext: terr.ErrorContext{},
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
		category: "nic",
		table:    unix.RT_TABLE_MAIN,
		filter: func(entry routeEntry) bool {
			return shouldOptimizeNICRoute(entry, nic)
		},
		params: newParams(1500, opt.initCwndSegments, opt.initRwndSegments, congctl),
		commonLogAttrs: []slog.Attr{
			slog.String("interface", nic),
			slog.String("congctl", congctl),
		},
		fetchOperation: "fetch_nic_routes",
		applyOperation: "optimize_nic_routes",
		commonErrContext: terr.ErrorContext{
			Interface: nic,
		},
	}
	return opt.optimize(ctx, job)
}

func (opt *Optimizer) optimize(ctx context.Context, job routeJob) error {
	routes, err := opt.fetchRoutes(ctx, job.table)
	if err != nil {
		if isContextError(err) {
			return err
		}
		return job.fetchError(err)
	}

	filtered := opt.filterRoutes(routes, job.filter)

	if opt.logger != nil {
		attrs := appendAttrs(job.commonLogAttrs,
			slog.Int("total_routes", len(filtered)),
		)
		opt.logger.Info(fmt.Sprintf("%s routes optimization started", job.category), terr.AttrsToArgs(attrs)...)
	}

	start := time.Now()
	optimized, skipped, applyErr := opt.applyRoutes(ctx, filtered, job.params, job.category)

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
		if isContextError(applyErr) {
			return applyErr
		}
		return job.applyError(applyErr)
	}
	return nil
}

type routeJob struct {
	category         string
	table            int
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

func (opt *Optimizer) fetchRoutes(ctx context.Context, table int) ([]routeEntry, error) {
	if opt.netlink == nil {
		return nil, fmt.Errorf("netlink client is nil")
	}

	routes, err := opt.netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	entries := make([]routeEntry, 0, len(routes))
	for _, rt := range routes {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		linkName := ""
		linkDown := false
		if rt.LinkIndex > 0 {
			if attrs, err := safeGetLinkAttrs(opt.netlink, rt.LinkIndex); err == nil {
				linkName = attrs.Name
				switch attrs.OperState {
				case netlink.OperDown, netlink.OperLowerLayerDown:
					linkDown = true
				}
			}
		}

		entries = append(entries, routeEntry{
			route:    rt,
			linkName: linkName,
			linkDown: linkDown,
		})
	}

	return entries, nil
}

func (opt *Optimizer) filterRoutes(routes []routeEntry, predicate routeFilter) []routeEntry {
	if predicate == nil || len(routes) == 0 {
		return routes
	}

	result := make([]routeEntry, 0, len(routes))
	for _, route := range routes {
		if predicate(route) {
			result = append(result, route)
		}
	}
	return result
}

func (opt *Optimizer) applyRoutes(ctx context.Context, routes []routeEntry, params params, category string) (int, int, error) {
	if len(routes) == 0 {
		return 0, 0, nil
	}

	optimized := 0
	failures := 0
	var firstErr error

	for _, entry := range routes {
		if ctx != nil && ctx.Err() != nil {
			return optimized, failures, ctx.Err()
		}

		if err := opt.applyRouteChange(ctx, entry.route, params); err != nil {
			if isContextError(err) {
				return optimized, failures, err
			}

			if firstErr == nil {
				firstErr = err
			}
			failures++

			if opt.logger != nil {
				opt.logger.Debug("route optimization skipped",
					slog.String("category", category),
					slog.String("route", summarizeRoute(entry)),
					slog.String("error", err.Error()))
			}
			continue
		}

		optimized++
		if opt.logger != nil {
			opt.logger.Debug("route optimization applied",
				slog.String("category", category),
				slog.String("route", summarizeRoute(entry)))
		}
	}

	return optimized, failures, firstErr
}

func (opt *Optimizer) applyRouteChange(ctx context.Context, route netlink.Route, params params) error {
	if opt.netlink == nil {
		return fmt.Errorf("netlink client is nil")
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	updated := route
	updated.MTU = params.mtu
	updated.InitCwnd = params.initCwnd
	updated.InitRwnd = params.initRwnd
	updated.FastOpenNoCookie = 1
	if params.congctl != "" {
		updated.Congctl = params.congctl
	}

	if err := opt.netlink.RouteReplace(&updated); err != nil {
		return fmt.Errorf("route replace: %w", err)
	}
	return nil
}

func summarizeRoute(entry routeEntry) string {
	dst := "default"
	if entry.route.Dst != nil {
		dst = entry.route.Dst.String()
	}
	gw := "-"
	if entry.route.Gw != nil {
		if gwStr := entry.route.Gw.String(); gwStr != "" {
			gw = gwStr
		}
	}
	dev := entry.linkName
	if dev == "" {
		dev = "-"
	}
	return fmt.Sprintf("dst=%s gw=%s dev=%s table=%d", dst, gw, dev, entry.route.Table)
}

func shouldOptimizeLocalRoute(entry routeEntry) bool {
	if entry.route.Table != unix.RT_TABLE_LOCAL || entry.linkName == "" {
		return false
	}
	if entry.linkName == "lo" {
		return false
	}
	if entry.linkDown {
		return false
	}
	if entry.route.Type == unix.RTN_BROADCAST || entry.route.Type == unix.RTN_MULTICAST {
		return false
	}
	return true
}

func shouldOptimizeLoopbackRoute(entry routeEntry) bool {
	return entry.route.Table == unix.RT_TABLE_LOCAL && entry.linkName == "lo"
}

func shouldOptimizeNICRoute(entry routeEntry, nic string) bool {
	if entry.route.Table != unix.RT_TABLE_MAIN || entry.linkName == "" {
		return false
	}
	if entry.linkName != nic {
		return false
	}
	if entry.linkDown {
		return false
	}
	if entry.route.Congctl != "" {
		return false
	}
	return true
}

// runIPCommand executes ip commands with timeout.
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

func (opt *Optimizer) fetchRouteLinesFromCommand(ctx context.Context, args ...string) ([]string, error) {
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

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

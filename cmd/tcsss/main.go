package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"tcsss/internal/app"
	configtemplates "tcsss/internal/config"
	"tcsss/internal/detector"
	"tcsss/internal/route"
	"tcsss/internal/syslimit"
	"tcsss/internal/traffic"
	"tcsss/internal/version"
)

func main() {
	var confDirFlag string
	var modeFlag string
	var showVersion bool

	flag.StringVar(&confDirFlag, "conf", "", "configuration directory path (default: /etc/tcsss)")
	flag.StringVar(&modeFlag, "mode", "", "traffic mode: client, server, or aggregate")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("tcsss version %s\n", version.Current)
		return
	}

	startTime := time.Now()

	legacyModeArg := ""
	if flag.NArg() > 0 {
		legacyModeArg = flag.Arg(0)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	stageDurations := map[string]time.Duration{}
	recordDuration := func(stage string, since time.Time) {
		stageDurations[stage] = time.Since(since)
	}

	templateDir, err := resolveTemplateDir(confDirFlag)
	if err != nil {
		logger.Error("failed to resolve template directory", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("using template directory", slog.String("path", templateDir))

	mode := strings.TrimSpace(modeFlag)
	if mode == "" && strings.TrimSpace(legacyModeArg) != "" {
		mode = legacyModeArg
		logger.Warn("legacy mode argument detected; use --mode flag instead", slog.String("argument", legacyModeArg))
	}

	ctx, cancel := signalContext(logger)
	defer cancel()

	kernelStart := time.Now()
	if err := detector.ValidateKernelModules(logger); err != nil {
		logger.Error("kernel module validation failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	recordDuration("kernel_modules", kernelStart)

	validationStart := time.Now()
	type memoryResult struct {
		info detector.MemoryInfo
		err  error
	}

	runtimeCh := make(chan error, 1)
	memoryCh := make(chan memoryResult, 1)

	go func() {
		runtimeCh <- detector.ValidateRuntime(logger)
	}()

	go func() {
		info, detectErr := detector.DetectMemoryInfo(logger)
		memoryCh <- memoryResult{info: info, err: detectErr}
	}()

	if err := <-runtimeCh; err != nil {
		logger.Error("runtime validation failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	var memInfo detector.MemoryInfo
	memResult := <-memoryCh
	if memResult.err != nil {
		logger.Warn("memory detection failed; proceeding with defaults", slog.String("error", memResult.err.Error()))
	} else {
		memInfo = memResult.info
	}

	recordDuration("runtime_and_memory", validationStart)

	configStart := time.Now()
	initConfig, err := configtemplates.LoadTrafficInitConfig(templateDir, mode)
	if err != nil {
		logger.Warn("falling back to default traffic template", slog.String("error", err.Error()), slog.String("fallback_mode", string(initConfig.Mode)))
	}
	logger.Info("traffic template applied", slog.String("mode", string(initConfig.Mode)))
	recordDuration("traffic_template_load", configStart)

	trafficSettings := traffic.Settings{
		Routes: route.WindowConfig{
			InitCwndBytes:       initConfig.InitCwndBytes,
			InitRwndBytes:       initConfig.InitRwndBytes,
			LoopbackWindowBytes: initConfig.InitLoopbackWindowBytes,
		},
	}

	sysctlApplier := syslimit.NewSysctlConfApplier(logger, templateDir, initConfig.Mode)

	limitsApplier := syslimit.NewLimitsConfApplier(logger, templateDir)

	trafficShaper := traffic.NewShaper(logger, trafficSettings)

	daemon := app.NewDaemon(app.Dependencies{
		SysctlApplier:  sysctlApplier,
		LimitsApplier:  limitsApplier,
		TrafficManager: trafficShaper,
		Logger:         logger,
	})

	logger.Info("startup checks completed",
		slog.Duration("total", time.Since(startTime)),
		slog.Duration("kernel_modules", stageDurations["kernel_modules"]),
		slog.Duration("runtime_and_memory", stageDurations["runtime_and_memory"]),
		slog.Duration("traffic_template_load", stageDurations["traffic_template_load"]),
		slog.Float64("memory_gb", memInfo.TotalGB),
		slog.String("memory_tier", memInfo.Tier.String()),
		slog.String("mode", string(initConfig.Mode)),
	)

	if err := daemon.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown complete")
			return
		}
		logger.Error("daemon terminated", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func signalContext(logger *slog.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		defer signal.Stop(signals)
		select {
		case sig := <-signals:
			if logger != nil {
				logger.Info("signal received, shutting down", slog.String("signal", sig.String()))
			}
			cancel()
			select {
			case sig := <-signals:
				if logger != nil {
					logger.Warn("second signal received, forcing exit", slog.String("signal", sig.String()))
				}
				os.Exit(1)
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		cancel()
		time.Sleep(50 * time.Millisecond)
	}
}

func resolveTemplateDir(confFlag string) (string, error) {
	if confFlag != "" {
		if err := validateTemplateDir(confFlag); err != nil {
			return "", fmt.Errorf("invalid template directory %q: %w", confFlag, err)
		}
		return confFlag, nil
	}

	if envDir := strings.TrimSpace(os.Getenv("TCSSS_CONFIG_DIR")); envDir != "" {
		if err := validateTemplateDir(envDir); err == nil {
			return envDir, nil
		}
	}

	defaultDir := "/etc/tcsss"
	if err := validateTemplateDir(defaultDir); err == nil {
		return defaultDir, nil
	}

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		localTemplates := filepath.Join(execDir, "templates")
		if err := validateTemplateDir(localTemplates); err == nil {
			return localTemplates, nil
		}
	}

	return "", fmt.Errorf("no valid template directory found")
}

func validateTemplateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	requiredFiles := []string{"common.conf"}
	for _, name := range requiredFiles {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("missing required file %s: %w", name, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	memoryPattern := regexp.MustCompile(`^limits_[0-9]+\.?[0-9]*(mb|gb|tb)\.conf$`)
	trafficFiles := map[string]struct{}{
		"1-client.conf":    {},
		"1-server.conf":    {},
		"1-aggregate.conf": {},
	}

	hasMemory := false
	hasTraffic := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if memoryPattern.MatchString(name) {
			hasMemory = true
		}
		if _, ok := trafficFiles[name]; ok {
			hasTraffic = true
		}
	}

	if !hasMemory {
		return fmt.Errorf("no memory tier configuration found in %s; provide at least one limits_*.conf file", dir)
	}

	if !hasTraffic {
		return fmt.Errorf("no traffic mode configuration found in %s; provide at least one of 1-client.conf, 1-server.conf, or 1-aggregate.conf", dir)
	}

	return nil
}

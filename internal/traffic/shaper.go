package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	terr "tcsss/internal/errors"
	route "tcsss/internal/route"
)

// Shaper orchestrates traffic shaping for network interfaces.
type Shaper struct {
	logger            *slog.Logger
	routeOptimizer    *route.Optimizer
	classifier        *InterfaceClassifier
	appliedMu         sync.RWMutex
	appliedSignatures map[string]string
	didInitialCleanup bool
	netlink           NetlinkClient
	executor          CommandExecutor
	reapplyInterval   time.Duration
	cleanupInterval   time.Duration
	applyTimeout      time.Duration
	profiles          profileSet
	ethtoolCache      map[string]*ethtoolCacheEntry
	ethtoolCacheMu    sync.RWMutex
	ethtoolCacheTTL   time.Duration
	watchMu           sync.Mutex
	watchCancel       context.CancelFunc
	watchDone         chan struct{}
}

// NewShaper constructs a traffic Shaper.
func NewShaper(logger *slog.Logger, settings Settings) *Shaper {
	return NewShaperWithDependencies(logger, settings, defaultNetlinkClient{}, processExecutor{})
}

// NewShaperWithDependencies constructs a traffic Shaper with injected dependencies.
func NewShaperWithDependencies(logger *slog.Logger, settings Settings, netlinkClient NetlinkClient, executor CommandExecutor) *Shaper {
	settings = settings.withDefaults()
	return &Shaper{
		logger: logger,
		routeOptimizer: route.NewOptimizer(logger, settings.Routes, route.Dependencies{
			Netlink:        netlinkClient,
			Executor:       executor,
			CommandTimeout: 0,
		}),
		classifier:        NewInterfaceClassifier(logger, netlinkClient),
		appliedSignatures: make(map[string]string),
		netlink:           netlinkClient,
		executor:          executor,
		reapplyInterval:   settings.Watcher.ReapplyInterval,
		cleanupInterval:   settings.Watcher.CleanupInterval,
		applyTimeout:      settings.Watcher.ApplyTimeout,
		profiles:          newProfileSet(settings.Profiles),
		ethtoolCache:      make(map[string]*ethtoolCacheEntry),
		ethtoolCacheTTL:   settings.EthtoolCacheTTL,
	}
}

// Apply configures traffic shaping for all relevant interfaces.
func (s *Shaper) Apply(ctx context.Context) error {
	// First, optimize routing tables for better TCP performance
	if err := s.routeOptimizer.Optimize(ctx); err != nil {
		if isContextError(err) {
			return err
		}

		optErr := fmt.Errorf("optimize routes: %w", err)
		s.handleCategorizedError("route optimization failed", "", terr.New(
			terr.CategoryRecoverable,
			optErr,
			terr.ErrorContext{Operation: "optimize_routes"},
		), terr.CategoryRecoverable)
		return optErr
	}

	return s.applyInterfaces(ctx, nil)
}

func (s *Shaper) handleCategorizedError(message, iface string, err error, defaultCategory terr.Category) {
	if s.logger == nil || err == nil {
		return
	}

	category := defaultCategory
	var ctxMap map[string]any
	if typed, ok := err.(*terr.Error); ok && typed != nil {
		category = typed.Category
		ctxMap = typed.Context.ToMap()
	}

	attrs := []slog.Attr{
		slog.String("category", category.String()),
		slog.String("error", err.Error()),
	}
	if iface != "" {
		attrs = append(attrs, slog.String("interface", iface))
	}
	if len(ctxMap) > 0 {
		attrs = append(attrs, slog.Any("context", ctxMap))
	}

	switch category {
	case terr.CategoryOptional:
		s.logger.Debug(message, terr.AttrsToArgs(attrs)...)
	default:
		s.logger.Error(message, terr.AttrsToArgs(attrs)...)
	}
}

func (s *Shaper) logOptional(message, iface string, err error, ctx terr.ErrorContext) {
	if err == nil {
		return
	}
	s.handleCategorizedError(message, iface, terr.New(terr.CategoryOptional, err, ctx), terr.CategoryOptional)
}

func wrapInterfaceError(err error, iface, operation string, extras terr.ErrorContext) error {
	if err == nil {
		return nil
	}
	ctx := terr.ErrorContext{Operation: operation, Interface: iface}.Merge(extras)
	return terr.New(terr.CategoryRecoverable, err, ctx)
}

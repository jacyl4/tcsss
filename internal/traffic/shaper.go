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
	}
}

// Apply configures traffic shaping for all relevant interfaces.
func (s *Shaper) Apply(ctx context.Context) error {
	// First, optimize routing tables for better TCP performance
	if err := s.routeOptimizer.Optimize(ctx); err != nil {
		s.handleCategorizedError("route optimization failed", "", terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("optimize routes: %w", err),
			terr.ErrorContext{Operation: "optimize_routes"},
		), terr.CategoryRecoverable)
		// Continue with traffic shaping even if route optimization fails
	}

	return s.applyInterfaces(ctx, nil)
}

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
)

// SysctlService defines system limit reconciliation behavior.
type SysctlService interface {
	Apply(ctx context.Context) error
}

// RlimitService defines process resource limit reconciliation behavior.
type RlimitService interface {
	Apply(ctx context.Context) error
}

// LimitsService defines system-wide resource limit reconciliation behavior.
type LimitsService interface {
	Apply(ctx context.Context) error
}

// TrafficService defines traffic shaping reconciliation behavior.
type TrafficService interface {
	Apply(ctx context.Context) error
	Watch(ctx context.Context) error
}

// Dependencies groups the external services required by the daemon.
type Dependencies struct {
	SysctlApplier  SysctlService
	RlimitApplier  RlimitService
	LimitsApplier  LimitsService
	TrafficManager TrafficService
	Logger         *slog.Logger
}

// Daemon coordinates subsystems and event loops.
type Daemon struct {
	sysctlApplier  SysctlService
	rlimitApplier  RlimitService
	limitsApplier  LimitsService
	trafficManager TrafficService
	logger         *slog.Logger
}

// NewDaemon constructs a Daemon with validated dependencies.
func NewDaemon(deps Dependencies) *Daemon {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &Daemon{
		sysctlApplier:  deps.SysctlApplier,
		rlimitApplier:  deps.RlimitApplier,
		limitsApplier:  deps.LimitsApplier,
		trafficManager: deps.TrafficManager,
		logger:         deps.Logger,
	}
}

// Run executes initialization and blocks until the context is cancelled.
func (d *Daemon) Run(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("daemon panic: %v", r)
			if d.logger != nil {
				d.logger.Error("daemon panic recovered",
					slog.Any("panic", r),
					slog.String("stack", string(stack)))
			}
		}
	}()

	if ctx == nil {
		return errors.New("context must not be nil")
	}

	// Priority 1: Apply kernel parameters (sysctl)
	// Foundation layer - network stack, connection limits, memory management
	// Must be applied first as it affects system-wide behavior
	if d.sysctlApplier != nil {
		if err := d.sysctlApplier.Apply(ctx); err != nil {
			d.logger.Error("sysctl apply failed", slog.String("error", err.Error()))
			return err
		}
	}

	// Priority 2: Apply system-wide resource limits (PAM/systemd/shell)
	// Affects future login sessions and service starts
	// Requires re-login or systemctl daemon-reexec to take effect
	if d.limitsApplier != nil {
		if err := d.limitsApplier.Apply(ctx); err != nil {
			d.logger.Error("limits apply failed", slog.String("error", err.Error()))
			return err
		}
	}

	// Priority 3: Apply current process resource limits (rlimit)
	// Immediate effect on running process - should be last
	// Ensures the daemon itself has proper limits
	if d.rlimitApplier != nil {
		if err := d.rlimitApplier.Apply(ctx); err != nil {
			d.logger.Error("rlimit apply failed", slog.String("error", err.Error()))
			return err
		}
	}

	// Priority 4: Apply traffic shaping and start watch loop
	var wg sync.WaitGroup
	watchErrs := make(chan error, 1)

	if d.trafficManager != nil {
		if err := d.trafficManager.Apply(ctx); err != nil {
			d.logger.Error("traffic apply failed", slog.String("error", err.Error()))
			return err
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.trafficManager.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case watchErrs <- err:
				default:
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-watchErrs:
		d.logger.Error("watch loop failed", slog.String("error", err.Error()))
		return err
	}

	wg.Wait()
	return ctx.Err()
}

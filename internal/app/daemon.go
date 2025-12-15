package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

// SysctlService defines system limit reconciliation behavior.
type SysctlService interface {
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
	StopWatch()
	WaitWatch(ctx context.Context) error
}

// Dependencies groups the external services required by the daemon.
type Dependencies struct {
	SysctlApplier  SysctlService
	LimitsApplier  LimitsService
	TrafficManager TrafficService
	Logger         *slog.Logger
	ReadyNotifier  ReadyNotifier
}

// Daemon coordinates subsystems and event loops.
type Daemon struct {
	sysctlApplier  SysctlService
	limitsApplier  LimitsService
	trafficManager TrafficService
	logger         *slog.Logger
	readyNotifier  ReadyNotifier
}

// NewDaemon constructs a Daemon with validated dependencies.
func NewDaemon(deps Dependencies) *Daemon {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	if deps.ReadyNotifier == nil {
		deps.ReadyNotifier = systemdNotifier{}
	}
	return &Daemon{
		sysctlApplier:  deps.SysctlApplier,
		limitsApplier:  deps.LimitsApplier,
		trafficManager: deps.TrafficManager,
		logger:         deps.Logger,
		readyNotifier:  deps.ReadyNotifier,
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

	// Priority 3: Apply traffic shaping and start watch loop
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

	d.notifyReady()

	select {
	case <-ctx.Done():
		if d.logger != nil {
			d.logger.Info("shutdown signal received, stopping watchers")
		}
		if d.trafficManager != nil {
			d.trafficManager.StopWatch()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := d.trafficManager.WaitWatch(shutdownCtx); err != nil && d.logger != nil && !errors.Is(err, context.DeadlineExceeded) {
				d.logger.Warn("traffic watcher did not stop cleanly", slog.String("error", err.Error()))
			}
			cancel()
		}
	case err := <-watchErrs:
		d.logger.Error("watch loop failed", slog.String("error", err.Error()))
		return err
	}

	wg.Wait()
	return ctx.Err()
}

// ReadyNotifier abstracts systemd readiness notifications for easier testing.
type ReadyNotifier interface {
	NotifyReady() (bool, error)
}

type systemdNotifier struct{}

func (systemdNotifier) NotifyReady() (bool, error) {
	return daemon.SdNotify(false, daemon.SdNotifyReady)
}

func (d *Daemon) notifyReady() {
	if d.readyNotifier == nil {
		return
	}
	sent, err := d.readyNotifier.NotifyReady()
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("systemd readiness notification failed", slog.String("error", err.Error()))
		}
		return
	}
	if d.logger == nil {
		return
	}
	if sent {
		d.logger.Info("systemd notified: ready")
	} else {
		d.logger.Debug("systemd notify skipped (no NOTIFY_SOCKET)")
	}
}

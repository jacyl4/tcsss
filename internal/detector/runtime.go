package detector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	terr "tcsss/internal/errors"
)

var (
	requiredCommands = []string{"ip", "tc", "ethtool"}
	cakeModuleNames  = []string{"sch_cake", "cake"}
)

// ValidateRuntime ensures required binaries and kernel support are available before
// the traffic shaper is started. Returns a categorized critical error on failure.
func ValidateRuntime(logger *slog.Logger) error {
	if logger != nil {
		logger.Info("runtime prerequisite check started")
	}

	var issues []string

	for _, cmd := range requiredCommands {
		if _, err := exec.LookPath(cmd); err != nil {
			issues = append(issues, fmt.Sprintf("missing command %q: %v", cmd, err))
		}
	}

	if err := ensureCakeAvailable(); err != nil {
		issues = append(issues, err.Error())
	}

	if len(issues) > 0 {
		description := strings.Join(issues, "; ")
		if logger != nil {
			logger.Error("runtime prerequisite check failed", slog.String("issues", description))
		}
		return terr.New(
			terr.CategoryCritical,
			errors.New("runtime prerequisites missing"),
			terr.ErrorContext{Operation: "runtime_validation", Actual: description},
		)
	}

	if logger != nil {
		logger.Info("runtime prerequisite check passed")
	}
	return nil
}

func ensureCakeAvailable() error {
	for _, name := range cakeModuleNames {
		if _, err := os.Stat(fmt.Sprintf("/sys/module/%s", name)); err == nil {
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, "modprobe", "-n", "sch_cake").Run(); err == nil {
		return nil
	}
	if err := exec.CommandContext(ctx, "modprobe", "-n", "cake").Run(); err == nil {
		return nil
	}

	return fmt.Errorf("cake qdisc kernel module (sch_cake) is not available")
}

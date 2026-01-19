package detector

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

var requiredCommands = []string{"ip", "tc", "ethtool"}

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

	if len(issues) > 0 {
		description := strings.Join(issues, "; ")
		if logger != nil {
			logger.Error("runtime prerequisite check failed", slog.String("issues", description))
		}
		return fmt.Errorf("runtime prerequisites missing: %s", description)
	}

	if logger != nil {
		logger.Info("runtime prerequisite check passed")
	}
	return nil
}

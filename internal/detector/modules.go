package detector

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// ModuleInfo captures kernel module metadata and requirement status.
type ModuleInfo struct {
	Name        string
	Required    bool
	Description string
}

// RequiredModules enumerates the modules the daemon depends on.
var RequiredModules = []ModuleInfo{
	{
		Name:        "nf_conntrack",
		Required:    false,
		Description: "Connection tracking for NAT optimization",
	},
	{
		Name:        "ifb",
		Required:    true,
		Description: "Intermediate Functional Block for ingress shaping",
	},
	{
		Name:        "sch_cake",
		Required:    true,
		Description: "CAKE qdisc for traffic shaping",
	},
}

// ValidateKernelModules ensures required kernel modules are loaded, attempting to
// modprobe missing modules when possible.
func ValidateKernelModules(logger *slog.Logger) error {
	var errs []string

	for _, module := range RequiredModules {
		if err := ensureModule(module, logger); err != nil {
			if module.Required {
				errs = append(errs, fmt.Sprintf("%s: %v", module.Name, err))
			} else if logger != nil {
				logger.Warn("optional kernel module not available",
					slog.String("module", module.Name),
					slog.String("description", module.Description),
					slog.String("error", err.Error()))
			}
			continue
		}

		if logger != nil {
			logger.Info("kernel module ready",
				slog.String("module", module.Name),
				slog.String("description", module.Description))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("required kernel modules missing: %s", strings.Join(errs, ", "))
	}

	return nil
}

func ensureModule(module ModuleInfo, logger *slog.Logger) error {
	modulePath := fmt.Sprintf("/sys/module/%s", module.Name)
	if _, err := os.Stat(modulePath); err == nil {
		return nil
	}

	if logger != nil {
		logger.Debug("attempting to load kernel module", slog.String("module", module.Name))
	}

	cmd := exec.Command("modprobe", module.Name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("modprobe failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	if _, err := os.Stat(modulePath); err != nil {
		return fmt.Errorf("module %s not found after modprobe", module.Name)
	}

	return nil
}

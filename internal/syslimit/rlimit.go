package syslimit

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	tmpl "tcsss/internal/config"
)

// RlimitApplier manages process resource limits (ulimit).
// This applies limits immediately to the current process via setrlimit() syscall.
// Priority: Highest - directly modifies running process limits.
type RlimitApplier struct {
	logger      *slog.Logger
	templateDir string
}

// NewRlimitApplier creates a new RlimitApplier instance.
func NewRlimitApplier(logger *slog.Logger, templateDir string) *RlimitApplier {
	return &RlimitApplier{logger: logger, templateDir: templateDir}
}

// limitConfig holds soft and hard limit values.
type limitConfig struct {
	resource int
	name     string
	soft     uint64
	hard     uint64
}

const unlimited = ^uint64(0) // RLIM_INFINITY

// resourceNameToRlimit maps rlimit resource names to their unix constants
var resourceNameToRlimit = map[string]int{
	"nofile":     unix.RLIMIT_NOFILE,
	"nproc":      unix.RLIMIT_NPROC,
	"core":       unix.RLIMIT_CORE,
	"stack":      unix.RLIMIT_STACK,
	"cpu":        unix.RLIMIT_CPU,
	"memlock":    unix.RLIMIT_MEMLOCK,
	"as":         unix.RLIMIT_AS,
	"data":       unix.RLIMIT_DATA,
	"fsize":      unix.RLIMIT_FSIZE,
	"msgqueue":   unix.RLIMIT_MSGQUEUE,
	"sigpending": unix.RLIMIT_SIGPENDING,
	"locks":      unix.RLIMIT_LOCKS,
}

// parseRlimitValue converts a string value to uint64, supporting "unlimited"
func parseRlimitValue(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "unlimited" {
		return unlimited, nil
	}

	val, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid rlimit value %q: %w", value, err)
	}
	return val, nil
}

// parseRlimitConfig parses rlimit configuration from template content.
// Only extracts rlimit.* entries, skipping comments and other parameters.
// Format: rlimit.<resource>=<value>
func (rla *RlimitApplier) parseRlimitConfig(content string) []limitConfig {
	var limits []limitConfig

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)

		// Skip non-rlimit lines
		if !strings.HasPrefix(line, "rlimit.") {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		resourceName := strings.TrimSpace(strings.TrimPrefix(parts[0], "rlimit."))
		valueStr := strings.TrimSpace(parts[1])

		// Map to unix constant
		resource, ok := resourceNameToRlimit[resourceName]
		if !ok {
			continue
		}

		// Parse value
		value, err := parseRlimitValue(valueStr)
		if err != nil {
			continue
		}

		// Stack values in templates are in KB, convert to bytes
		if resourceName == "stack" && value != unlimited {
			value *= 1024
		}

		limits = append(limits, limitConfig{
			resource: resource,
			name:     "RLIMIT_" + strings.ToUpper(resourceName),
			soft:     value,
			hard:     value,
		})
	}

	return limits
}

// Apply sets resource limits via setrlimit() syscall for the current process.
// Automatically detects system memory tier and applies appropriate limits.
// Only modifies limits that are explicitly defined in templates.
func (rla *RlimitApplier) Apply(ctx context.Context) error {
	// Detect memory tier and load templates
	templates, err := tmpl.DetectTemplateSet(rla.templateDir)
	if err != nil {
		rla.logger.Warn("memory detection failed, using default tier",
			slog.String("error", err.Error()))
	}

	rla.logger.Info("applying rlimit configuration",
		slog.String("memory_tier", templates.MemoryConfig.MemoryLabel))

	// Parse and merge limits (tier-specific overrides common)
	limitsMap := make(map[int]limitConfig)
	for _, limit := range rla.parseRlimitConfig(templates.Common) {
		limitsMap[limit.resource] = limit
	}
	for _, limit := range rla.parseRlimitConfig(templates.Specific) {
		limitsMap[limit.resource] = limit
	}

	// Apply limits
	applied := 0
	for _, limit := range limitsMap {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := rla.setLimit(limit); err != nil {
			rla.logger.Warn("setrlimit failed",
				slog.String("resource", limit.name),
				slog.String("error", err.Error()))
			continue
		}

		rla.logger.Debug("rlimit set",
			slog.String("resource", limit.name),
			slog.Uint64("value", limit.soft))
		applied++
	}

	rla.logger.Info("rlimit applied",
		slog.Int("count", applied))

	return nil
}

func (rla *RlimitApplier) setLimit(limit limitConfig) error {
	var current unix.Rlimit
	if err := unix.Getrlimit(limit.resource, &current); err != nil {
		return err
	}

	// Skip if already set
	if current.Cur == limit.soft && current.Max == limit.hard {
		return nil
	}

	// Apply new limit
	return unix.Setrlimit(limit.resource, &unix.Rlimit{
		Cur: limit.soft,
		Max: limit.hard,
	})
}

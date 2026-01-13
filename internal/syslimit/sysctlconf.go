package syslimit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	tmpl "tcsss/internal/config"
)

const (
	sysctlConfPath = "/etc/sysctl.conf"
	filePerm       = 0o600
)

// writeFileWithSync truncates the target file, writes the payload, and fsyncs it.
func writeFileWithSync(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	if err := file.Chmod(perm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	return nil
}

// reloadSysctl runs `sysctl --system` so new parameters take effect, returning trimmed output.
func reloadSysctl(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "sysctl", "--system")
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed != "" {
			return trimmed, fmt.Errorf("sysctl --system failed: %w: %s", err, trimmed)
		}
		return trimmed, fmt.Errorf("sysctl --system failed: %w", err)
	}

	return trimmed, nil
}

// SysctlConfApplier writes sysctl configuration from templates.
type SysctlConfApplier struct {
	logger      *slog.Logger
	path        string
	mode        tmpl.TrafficMode
	templateDir string
}

// NewSysctlConfApplier creates a new applier.
func NewSysctlConfApplier(logger *slog.Logger, templateDir string, mode tmpl.TrafficMode) *SysctlConfApplier {
	if mode == "" {
		mode = tmpl.TrafficModeClient
	}
	return &SysctlConfApplier{
		logger:      logger,
		path:        sysctlConfPath,
		mode:        mode,
		templateDir: templateDir,
	}
}

// SetSysctlPath overrides the default path (for testing).
func (sca *SysctlConfApplier) SetSysctlPath(path string) {
	if path != "" {
		sca.path = path
	}
}

// Apply writes sysctl.conf from templates based on memory tier.
func (sca *SysctlConfApplier) Apply(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tplSet, detectErr := tmpl.DetectTemplateSet(sca.templateDir)
	sca.logDetectionFallback(detectErr)

	params, err := sca.buildTemplateParameters(tplSet)
	if err != nil {
		return err
	}

	existing := sca.loadExistingConfig()
	merged := merge(existing, params)

	if sca.isConfigUnchanged(existing, merged) {
		sca.logConfigUnchanged()
		return nil
	}

	if err := sca.writeConfigAndReload(ctx, merged, params, tplSet); err != nil {
		return err
	}

	if err := sca.setTransparentHugepage(ctx); err != nil {
		if sca.logger != nil {
			sca.logger.Warn("failed to set transparent hugepage", slog.String("error", err.Error()))
		}
		// Non-fatal: continue even if this fails
	}

	return nil
}

func (sca *SysctlConfApplier) logDetectionFallback(err error) {
	if err != nil && sca.logger != nil {
		sca.logger.Warn("failed to detect memory, using default tier", slog.String("error", err.Error()))
	}
}

func (sca *SysctlConfApplier) buildTemplateParameters(tplSet tmpl.TemplateSet) (map[string]string, error) {
	roleTemplate, err := tmpl.TrafficTemplateContent(sca.templateDir, sca.mode)
	if err != nil {
		return nil, fmt.Errorf("load traffic template: %w", err)
	}
	params := parseTemplate(tplSet.Common, tplSet.Specific, roleTemplate)
	if len(params) == 0 {
		return nil, fmt.Errorf("no parameters in templates")
	}
	return params, nil
}

func (sca *SysctlConfApplier) loadExistingConfig() string {
	data, err := os.ReadFile(sca.path)
	if err != nil {
		return ""
	}
	return string(data)
}

func (sca *SysctlConfApplier) isConfigUnchanged(existing, merged string) bool {
	return existing == merged
}

func (sca *SysctlConfApplier) logConfigUnchanged() {
	if sca.logger != nil {
		sca.logger.Info("sysctl.conf already up to date")
	}
}

func (sca *SysctlConfApplier) writeConfigAndReload(ctx context.Context, merged string, params map[string]string, tplSet tmpl.TemplateSet) error {
	if err := writeFileWithSync(sca.path, []byte(merged), filePerm); err != nil {
		return fmt.Errorf("persist sysctl.conf: %w", err)
	}

	if sca.logger != nil {
		sca.logger.Info("sysctl.conf updated",
			slog.Int("params", len(params)),
			slog.String("memory_tier", tplSet.MemoryConfig.MemoryLabel),
			slog.Float64("system_memory_gb", tplSet.SystemMemoryGB),
			slog.Float64("effective_memory_gb", tplSet.EffectiveMemoryGB),
			slog.String("mode", string(sca.mode)))
	}

	output, err := reloadSysctl(ctx)
	if err := sca.handleReloadResult(output, err); err != nil {
		return err
	}

	return nil
}

func (sca *SysctlConfApplier) handleReloadResult(output string, err error) error {
	if err != nil {
		if strings.Contains(output, "sysctl: cannot stat") {
			if sca.logger != nil {
				sca.logger.Warn("sysctl apply completed with missing kernel parameters",
					slog.String("details", output))
			}
			return nil
		}
		return err
	}

	if sca.logger != nil && strings.TrimSpace(output) != "" {
		sca.logger.Debug("sysctl --system output", slog.String("details", strings.TrimSpace(output)))
	}
	return nil
}

// parseTemplate extracts key=value pairs from templates.
// Skips rlimit.* parameters as they are handled by separate appliers.
func parseTemplate(templates ...string) map[string]string {
	params := make(map[string]string)

	for _, tpl := range templates {
		for _, line := range strings.Split(tpl, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Remove inline comments
			if idx := strings.Index(line, "#"); idx > 0 {
				line = strings.TrimSpace(line[:idx])
			}

			// Parse key = value
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			if key == "" || strings.HasPrefix(key, "rlimit.") || !isSysctlKey(key) {
				// Skip non-sysctl parameters (handled by other appliers)
				continue
			}

			value := strings.TrimSpace(parts[1])
			params[key] = value
		}
	}

	return params
}

// isSysctlKey returns true when the key looks like a kernel parameter.
func isSysctlKey(key string) bool {
	if strings.HasPrefix(key, "rlimit.") {
		return false
	}
	// Sysctl keys always use dot-separated namespace (e.g. net.ipv4.tcp_sack)
	if !strings.Contains(key, ".") {
		return false
	}
	// Whitespace within the key would be invalid in sysctl.conf.
	return !strings.ContainsAny(key, " \t")
}

// merge updates template parameters in existing config, preserving other lines.
func merge(existing string, params map[string]string) string {
	if existing == "" {
		// No existing config, create new
		var lines []string
		lines = append(lines, "# tcsss managed sysctl parameters")
		for key, value := range params {
			lines = append(lines, fmt.Sprintf("%s = %s", key, value))
		}
		return strings.Join(lines, "\n") + "\n"
	}

	var output []string
	updated := make(map[string]bool)

	// Process existing lines
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			output = append(output, line)
			continue
		}

		// Parse key
		key := extractKey(trimmed)
		if key == "" {
			output = append(output, line)
			continue
		}

		// If this key is in template, update it
		if value, found := params[key]; found {
			output = append(output, fmt.Sprintf("%s = %s", key, value))
			updated[key] = true
		} else {
			// Keep original line
			output = append(output, line)
		}
	}

	// Append missing parameters
	var missing []string
	for key, value := range params {
		if !updated[key] {
			missing = append(missing, fmt.Sprintf("%s = %s", key, value))
		}
	}

	if len(missing) > 0 {
		output = append(output, "")
		output = append(output, "# tcsss managed parameters")
		output = append(output, missing...)
	}

	result := strings.Join(output, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	return result
}

// extractKey extracts the parameter key from a config line.
func extractKey(line string) string {
	// Remove inline comment
	if idx := strings.Index(line, "#"); idx > 0 {
		line = strings.TrimSpace(line[:idx])
	}

	// Parse key = value or key value
	if idx := strings.Index(line, "="); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}

	// Try space-separated format
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return fields[0]
	}

	return ""
}

// setTransparentHugepage configures transparent hugepage to madvise mode.
// madvise mode allows applications to selectively use hugepages via madvise(2).
func (sca *SysctlConfApplier) setTransparentHugepage(ctx context.Context) error {
	const (
		thpPath = "/sys/kernel/mm/transparent_hugepage/enabled"
		mode    = "madvise"
	)

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check if the file exists
	if _, err := os.Stat(thpPath); os.IsNotExist(err) {
		return fmt.Errorf("transparent hugepage not supported: %s does not exist", thpPath)
	}

	// Write the mode
	if err := os.WriteFile(thpPath, []byte(mode+"\n"), 0644); err != nil {
		return fmt.Errorf("write %s: %w", thpPath, err)
	}

	if sca.logger != nil {
		sca.logger.Info("transparent hugepage configured", slog.String("mode", mode))
	}

	return nil
}

package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"tcsss/internal/sysinfo"
)

// TrafficMode represents the runtime profile selected for traffic tuning.
type TrafficMode string

const (
	// TrafficModeClient applies the client-side template.
	TrafficModeClient TrafficMode = "client"
	// TrafficModeServer applies the server-side template.
	TrafficModeServer TrafficMode = "server"
	// TrafficModeAggregate applies the aggregate template.
	TrafficModeAggregate TrafficMode = "aggregate"
)

// TrafficInitConfig contains the TCP window tuning parameters extracted from templates.
type TrafficInitConfig struct {
	Mode                    TrafficMode
	InitCwndBytes           int
	InitRwndBytes           int
	InitLoopbackWindowBytes int
}

var (
	trafficModeFiles = map[TrafficMode]string{
		TrafficModeClient:    "1-client.conf",
		TrafficModeServer:    "1-server.conf",
		TrafficModeAggregate: "1-aggregate.conf",
	}

	trafficFilenameToMode = map[string]TrafficMode{
		"1-client.conf":    TrafficModeClient,
		"1-server.conf":    TrafficModeServer,
		"1-aggregate.conf": TrafficModeAggregate,
	}

	trafficModePriority = map[TrafficMode]int{
		TrafficModeClient:    1,
		TrafficModeServer:    2,
		TrafficModeAggregate: 3,
	}

	memoryTierPattern = regexp.MustCompile(`^limits_([0-9]+\.?[0-9]*)(mb|gb|tb)\.conf$`)

	defaultTrafficInitConfig = TrafficInitConfig{
		Mode:                    TrafficModeClient,
		InitCwndBytes:           1024 * 1460,
		InitRwndBytes:           3 * 1024 * 1024,
		InitLoopbackWindowBytes: 10 * 1024 * 1024,
	}
)

// MemoryTierConfig represents a dynamically loaded memory tier template.
type MemoryTierConfig struct {
	MemoryMB    float64
	MemoryLabel string
	Filename    string
	Content     string
}

// TemplateSet bundles the selected templates for sysctl and limits generation.
type TemplateSet struct {
	Common            string
	Specific          string
	MemoryConfig      MemoryTierConfig
	SystemMemoryGB    float64
	EffectiveMemoryGB float64
}

// LoadTrafficInitConfig reads and parses the traffic tuning template for the requested mode.
// When mode is empty, it auto-detects the highest priority template present in the directory.
func LoadTrafficInitConfig(templateDir, mode string) (TrafficInitConfig, error) {
	trimmed := strings.TrimSpace(mode)
	var selectedMode TrafficMode
	if trimmed != "" {
		var recognized bool
		selectedMode, recognized = normalizeTrafficMode(trimmed)
		if !recognized {
			return defaultTrafficInitConfig, fmt.Errorf("unsupported traffic mode %q", mode)
		}
	} else {
		var err error
		selectedMode, err = detectTrafficModeFromFiles(templateDir)
		if err != nil {
			return defaultTrafficInitConfig, err
		}
	}

	content, err := TrafficTemplateContent(templateDir, selectedMode)
	if err != nil {
		return defaultTrafficInitConfig, err
	}

	cfg, err := parseTrafficTemplate(selectedMode, content)
	if err != nil {
		return defaultTrafficInitConfig, err
	}

	return cfg, nil
}

// TrafficTemplateContent returns the raw template content for the given traffic mode.
func TrafficTemplateContent(templateDir string, mode TrafficMode) (string, error) {
	if filename, ok := trafficModeFiles[mode]; ok {
		return readTemplateFile(templateDir, filename)
	}
	if filename, ok := trafficModeFiles[TrafficModeClient]; ok {
		return readTemplateFile(templateDir, filename)
	}
	return "", fmt.Errorf("traffic mode %q is not supported", mode)
}

// DetectTemplateSet selects the appropriate sysctl templates based on system memory.
func DetectTemplateSet(templateDir string) (TemplateSet, error) {
	memKB, err := sysinfo.ReadMemoryKB("/proc/meminfo")
	if err != nil {
		return TemplateSet{}, fmt.Errorf("detect system memory: %w", err)
	}

	tiers, err := scanMemoryTierConfigs(templateDir)
	if err != nil {
		return TemplateSet{}, err
	}

	systemMemoryMB := float64(memKB) / 1024
	selectedTier, effectiveMB, err := selectBestMemoryTier(systemMemoryMB, tiers)
	if err != nil {
		return TemplateSet{}, err
	}

	commonContent, err := readTemplateFile(templateDir, "common.conf")
	if err != nil {
		return TemplateSet{}, fmt.Errorf("load common template: %w", err)
	}

	if err := loadMemoryTierContent(templateDir, &selectedTier); err != nil {
		return TemplateSet{}, err
	}

	return TemplateSet{
		Common:            commonContent,
		Specific:          selectedTier.Content,
		MemoryConfig:      selectedTier,
		SystemMemoryGB:    systemMemoryMB / 1024,
		EffectiveMemoryGB: effectiveMB / 1024,
	}, nil
}

func normalizeTrafficMode(mode string) (TrafficMode, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "c", string(TrafficModeClient):
		return TrafficModeClient, true
	case "s", string(TrafficModeServer):
		return TrafficModeServer, true
	case "a", "agg", string(TrafficModeAggregate):
		return TrafficModeAggregate, true
	default:
		return TrafficModeClient, false
	}
}

func detectTrafficModeFromFiles(templateDir string) (TrafficMode, error) {
	entries, err := os.ReadDir(templateDir)
	if err != nil {
		return "", fmt.Errorf("read template directory: %w", err)
	}

	bestPriority := math.MaxInt
	var bestMode TrafficMode

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := strings.ToLower(entry.Name())
		if mode, ok := trafficFilenameToMode[name]; ok {
			if priority, ok := trafficModePriority[mode]; ok && priority < bestPriority {
				bestPriority = priority
				bestMode = mode
			}
		}
	}

	if bestPriority == math.MaxInt {
		return "", errors.New("no traffic mode configuration found; ensure at least one of 1-client.conf, 1-server.conf, or 1-aggregate.conf exists")
	}

	return bestMode, nil
}

func parseTrafficTemplate(mode TrafficMode, content string) (TrafficInitConfig, error) {
	cfg := TrafficInitConfig{
		Mode: mode,
	}

	values := map[string]*int{
		"initCwndBytes":           &cfg.InitCwndBytes,
		"initRwndBytes":           &cfg.InitRwndBytes,
		"initLoopbackWindowBytes": &cfg.InitLoopbackWindowBytes,
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		ptr, ok := values[key]
		if !ok {
			continue
		}

		valueExpr := stripInlineComment(parts[1])
		result, err := evaluateExpression(valueExpr)
		if err != nil {
			return defaultTrafficInitConfig, fmt.Errorf("parse %s: %w", key, err)
		}
		*ptr = result
	}

	if cfg.InitCwndBytes == 0 {
		cfg.InitCwndBytes = defaultTrafficInitConfig.InitCwndBytes
	}
	if cfg.InitRwndBytes == 0 {
		cfg.InitRwndBytes = defaultTrafficInitConfig.InitRwndBytes
	}
	if cfg.InitLoopbackWindowBytes == 0 {
		cfg.InitLoopbackWindowBytes = defaultTrafficInitConfig.InitLoopbackWindowBytes
	}

	return cfg, nil
}

func stripInlineComment(value string) string {
	v := strings.TrimSpace(value)
	if idx := strings.Index(v, "#"); idx >= 0 {
		v = v[:idx]
	}
	return strings.TrimSpace(v)
}

func evaluateExpression(expr string) (int, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}

	factors := strings.Split(expr, "*")
	result := 1.0

	for _, factor := range factors {
		part := strings.TrimSpace(factor)
		if part == "" {
			return 0, fmt.Errorf("invalid factor in %q", expr)
		}

		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q: %w", part, err)
		}

		result *= value
	}

	if result < 0 {
		return 0, fmt.Errorf("negative result %f", result)
	}

	return int(math.Round(result)), nil
}

func readTemplateFile(templateDir, filename string) (string, error) {
	path := filepath.Join(templateDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read template file %s: %w", filename, err)
	}
	return string(data), nil
}

func scanMemoryTierConfigs(templateDir string) ([]MemoryTierConfig, error) {
	entries, err := os.ReadDir(templateDir)
	if err != nil {
		return nil, fmt.Errorf("read template directory: %w", err)
	}

	var configs []MemoryTierConfig

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := strings.ToLower(entry.Name())
		matches := memoryTierPattern.FindStringSubmatch(filename)
		if matches == nil {
			continue
		}

		sizeStr := matches[1]
		unit := matches[2]

		size, err := strconv.ParseFloat(sizeStr, 64)
		if err != nil {
			continue
		}

		var memoryMB float64
		switch unit {
		case "mb":
			memoryMB = size
		case "gb":
			memoryMB = size * 1024
		case "tb":
			memoryMB = size * 1024 * 1024
		default:
			continue
		}

		label := fmt.Sprintf("%s%s", sizeStr, unit)

		configs = append(configs, MemoryTierConfig{
			MemoryMB:    memoryMB,
			MemoryLabel: label,
			Filename:    entry.Name(),
		})
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no memory tier configuration found in %s; ensure at least one limits_*.conf exists", templateDir)
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].MemoryMB < configs[j].MemoryMB
	})

	return configs, nil
}

func selectBestMemoryTier(systemMemoryMB float64, tiers []MemoryTierConfig) (MemoryTierConfig, float64, error) {
	if len(tiers) == 0 {
		return MemoryTierConfig{}, 0, errors.New("no memory tier configurations available")
	}

	if systemMemoryMB <= 0 {
		return MemoryTierConfig{}, 0, fmt.Errorf("invalid system memory: %.2f MB", systemMemoryMB)
	}

	if systemMemoryMB > MaximumSupportedMemoryMB {
		return MemoryTierConfig{}, 0, fmt.Errorf("system memory %.2f MB exceeds supported range", systemMemoryMB)
	}

	effectiveMemoryMB := systemMemoryMB * MemoryEffectivenessFactor

	for i := len(tiers) - 1; i >= 0; i-- {
		if tiers[i].MemoryMB <= effectiveMemoryMB {
			return tiers[i], effectiveMemoryMB, nil
		}
	}

	return tiers[0], effectiveMemoryMB, nil
}

func loadMemoryTierContent(templateDir string, config *MemoryTierConfig) error {
	if config.Content != "" {
		return nil
	}

	content, err := readTemplateFile(templateDir, config.Filename)
	if err != nil {
		return fmt.Errorf("load memory tier config %s: %w", config.Filename, err)
	}

	config.Content = content
	return nil
}

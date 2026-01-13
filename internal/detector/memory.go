package detector

import (
	"sync"

	"tcsss/internal/sysinfo"
)

// MemoryTier represents different memory size categories
type MemoryTier int

const (
	MemoryTier1GB  MemoryTier = iota // ≤1GB RAM
	MemoryTier4GB                    // ≤4GB RAM
	MemoryTier8GB                    // ≤8GB RAM
	MemoryTier12GB                   // >8GB RAM (12GB+)
)

var (
	// Global cache for memory detection result
	cachedTier     MemoryTier
	cachedTierOnce sync.Once
	cachedTierErr  error
)

// DetectMemoryTier returns the memory tier classification based on system memory.
// Results are cached globally after the first call for maximum performance.
// This is the only API needed for memory tier detection.
//
// Memory tier thresholds (conservative to account for system overhead):
//   - MemoryTier1GB:  < 1.5 GB (actual 1GB systems)
//   - MemoryTier4GB:  < 5.0 GB (actual 4GB systems)
//   - MemoryTier8GB:  < 10.0 GB (actual 8GB systems)
//   - MemoryTier12GB: >= 10.0 GB (actual 12GB+ systems)
func DetectMemoryTier() (MemoryTier, error) {
	cachedTierOnce.Do(func() {
		memKB, err := sysinfo.ReadMemoryKB("/proc/meminfo")
		if err != nil {
			cachedTierErr = err
			cachedTier = MemoryTier1GB // Default to lowest tier on error
			return
		}

		memGB := float64(memKB) / (1024 * 1024)
		cachedTier = classifyMemoryTier(memGB)
	})
	return cachedTier, cachedTierErr
}

// classifyMemoryTier determines the memory tier based on available system memory (in GB).
// Uses conservative thresholds to account for system overhead and reserved memory.
func classifyMemoryTier(memoryGB float64) MemoryTier {
	switch {
	case memoryGB < 1.5: // Covers actual 1GB systems (may show as 0.9-1.2GB)
		return MemoryTier1GB
	case memoryGB < 5.0: // Covers actual 4GB systems (may show as 3.6-4.0GB)
		return MemoryTier4GB
	case memoryGB < 10.0: // Covers actual 8GB systems (may show as 7.2-8.0GB)
		return MemoryTier8GB
	default: // 10GB+ (actual 12GB+ systems)
		return MemoryTier12GB
	}
}

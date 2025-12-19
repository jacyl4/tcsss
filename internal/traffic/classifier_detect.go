package traffic

import (
	"path/filepath"
	"strings"
)

// isVirtualInterface detects if an interface is virtual (vs. physical hardware).
// The result influences ethtool offload settings (GRO on/off).
func (ic *InterfaceClassifier) isVirtualInterface(name string) bool {
	if name == "" {
		return false
	}

	ic.mu.RLock()
	if cached, ok := ic.virtualCache[name]; ok {
		ic.mu.RUnlock()
		return cached
	}
	ic.mu.RUnlock()

	if hasVirtualPrefix(name) {
		ic.cacheVirtualResult(name, true)
		return true
	}

	sysfsPath := filepath.Join("/sys/class/net", name)
	if resolved, err := filepath.EvalSymlinks(sysfsPath); err == nil && isSysfsVirtualPath(resolved) {
		ic.cacheVirtualResult(name, true)
		return true
	}

	ic.cacheVirtualResult(name, false)
	return false
}

func (ic *InterfaceClassifier) cacheVirtualResult(name string, isVirtual bool) {
	ic.mu.Lock()
	if ic.virtualCache == nil {
		ic.virtualCache = make(map[string]bool)
	}
	ic.virtualCache[name] = isVirtual
	ic.mu.Unlock()
}

// isSysfsVirtualPath checks if the resolved sysfs path indicates a virtual device.
func isSysfsVirtualPath(resolvedPath string) bool {
	lower := strings.ToLower(resolvedPath)
	return strings.Contains(lower, "/sys/devices/virtual/")
}

package traffic

import (
	"os"
	"path/filepath"
	"strings"
)

// isVirtualInterface detects if an interface is virtual (vs. physical hardware).
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

	isVirtual := ic.detectVirtualHardware(name)

	ic.mu.Lock()
	if ic.virtualCache == nil {
		ic.virtualCache = make(map[string]bool)
	}
	ic.virtualCache[name] = isVirtual
	ic.mu.Unlock()

	return isVirtual
}

func (ic *InterfaceClassifier) detectVirtualHardware(name string) bool {
	// Check name patterns (fast path)
	if hasInternalVirtualPrefix(name) || hasExternalVirtualPrefix(name) {
		return true
	}

	sysfsPath := filepath.Join("/sys/class/net", name)

	if resolved, err := filepath.EvalSymlinks(sysfsPath); err == nil {
		if isSysfsVirtualPath(resolved) {
			return true
		}
	}

	if driver := interfaceDriverModule(sysfsPath); driver != "" {
		if _, ok := virtualDriverModules[normalizeIdentifier(driver)]; ok {
			return true
		}
	}

	if vendor := interfaceVendor(sysfsPath); vendor != "" {
		if _, ok := virtualVendorIDs[normalizeIdentifier(vendor)]; ok {
			return true
		}
	}

	return false
}

// isSysfsVirtualPath checks if the resolved sysfs path indicates a virtual device.
func isSysfsVirtualPath(resolvedPath string) bool {
	lower := strings.ToLower(resolvedPath)

	// Standard virtual device path
	if strings.Contains(lower, "/sys/devices/virtual/") {
		return true
	}

	// VirtIO devices (check path component, not substring)
	pathSegments := strings.Split(lower, "/")
	for _, segment := range pathSegments {
		if segment == "virtio" || strings.HasPrefix(segment, "virtio") {
			return true
		}
		if segment == "vmbus" {
			return true
		}
	}

	return false
}

// interfaceDriverModule extracts the kernel driver module name.
func interfaceDriverModule(sysfsPath string) string {
	// Standard modular driver: /sys/class/net/{iface}/device/driver/module
	if module := readLinkBase(filepath.Join(sysfsPath, "device/driver/module")); module != "" {
		return module
	}
	// Built-in driver: /sys/class/net/{iface}/device/driver
	return readLinkBase(filepath.Join(sysfsPath, "device/driver"))
}

// interfaceVendor reads the PCI vendor ID from sysfs.
func interfaceVendor(sysfsPath string) string {
	data, err := os.ReadFile(filepath.Join(sysfsPath, "device/vendor"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readLinkBase returns the basename of a symlink target.
func readLinkBase(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// normalizeIdentifier canonicalizes vendor IDs and driver names.
func normalizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ToLower(value)
	// Handle both underscore and hyphen delimiters (some systems report hv-netvsc)
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

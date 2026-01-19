package traffic

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tcsss/internal/infra"

	"github.com/vishvananda/netlink"
)

// ifaceClass represents the classification of a network interface.
type ifaceClass int

const (
	classUnknown ifaceClass = iota
	classLoopback
	classExternalPhysical    // Physical interface that carries external traffic
	classExternalVirtual     // Virtual interface that carries external traffic
	classInternalVirtual     // Virtual interface that carries only internal traffic
	classInternalVirtualSkip // Virtual interface skipped entirely (matches skip prefixes)
)

const defaultExternalRefreshInterval = 30 * time.Second

var (
	// virtualVendorIDs maps PCI vendor IDs to virtualization platforms.
	// These IDs are read from /sys/class/net/{iface}/device/vendor.
	virtualVendorIDs = map[string]struct{}{
		"0x1414": {}, // Microsoft Hyper-V
		"0x15ad": {}, // VMware
		"0x1af4": {}, // Red Hat (VirtIO)
		"0x1d0f": {}, // Amazon Web Services (AWS)
		"0x1ae0": {}, // Google Cloud Platform (GCP)
		"0x1ec1": {}, // Alibaba Cloud
		"0x5853": {}, // XenSource (Xen hypervisor)
	}

	// virtualDriverModules identifies virtual NIC kernel drivers.
	// These are read from /sys/class/net/{iface}/device/driver/module.
	virtualDriverModules = map[string]struct{}{
		"ena":        {}, // AWS Elastic Network Adapter
		"gve":        {}, // Google Virtual Ethernet (GCP)
		"hv_netvsc":  {}, // Hyper-V Network Virtual Service Client
		"netvsc":     {}, // Legacy Hyper-V driver
		"virtio_net": {}, // VirtIO network driver (KVM/QEMU)
		"virtio_pci": {}, // VirtIO PCI transport
		"vmxnet3":    {}, // VMware vmxnet3 paravirtualized NIC
	}

	// internalVirtualPrefixes lists naming patterns for internal-only virtual interfaces.
	// These interfaces are skipped from TC configuration.
	internalVirtualPrefixes = []string{
		"br",     // Linux bridge (internal)
		"docker", // Docker container bridge
		"veth",   // Virtual Ethernet pair (container)
		"virbr",  // libvirt bridge
		"fwbr",   // Firewall bridge (OpenStack)
		"fwpr",   // Firewall provider (OpenStack)
		"fwln",   // Firewall link (OpenStack)
		"tap",
	}

	// externalVirtualPrefixes lists naming patterns for external-facing virtual interfaces.
	// These may carry external traffic and need TC configuration.
	externalVirtualPrefixes = []string{
		"tun",     // TUN device (VPN)
		"tap",     // TAP device (VPN)
		"wg",      // WireGuard VPN
		"zt",      // ZeroTier VPN
		"gre",     // GRE tunnel
		"gretap",  // GRE tunnel tap
		"sit",     // IPv6-in-IPv4 tunnel
		"vxlan",   // VXLAN overlay
		"macvlan", // MAC VLAN
		"macvtap", // MAC VLAN tap
		"ipvlan",  // IP VLAN
	}
)

// InterfaceClassifier provides interface classification with routing awareness.
type InterfaceClassifier struct {
	logger        *slog.Logger
	netlinkClient infra.NetlinkClient

	mu                  sync.RWMutex
	externalLinkIndexes map[int]struct{} // link index -> has default route
	virtualCache        map[string]bool  // interface name -> is virtual
	lastRefresh         time.Time
	refreshInterval     time.Duration
}

// NewInterfaceClassifier creates a new classifier.
func NewInterfaceClassifier(logger *slog.Logger, netlinkClient infra.NetlinkClient) *InterfaceClassifier {
	return &InterfaceClassifier{
		logger:              logger,
		netlinkClient:       netlinkClient,
		externalLinkIndexes: make(map[int]struct{}),
		virtualCache:        make(map[string]bool),
		refreshInterval:     defaultExternalRefreshInterval,
	}
}

// Classify determines the class of a network interface.
//
// Classification priority:
//  1. Loopback check (highest priority)
//  2. Internal skip patterns (exclude internal-only virtual interfaces)
//  3. External communication check (interfaces with default routes)
//  4. Virtual/Physical hardware detection (based on driver and device type)
//
// Classification affects which traffic shaping profile is applied:
//   - classLoopback: localhost interface (lo), high MTU and aggressive tuning
//   - classExternalPhysical: physical NICs handling internet traffic
//   - classExternalVirtual: virtual interfaces (docker, veth) carrying external traffic
//   - classInternalVirtual: virtual interfaces for container/VM internal networks
//   - classInternalVirtualSkip: ignored virtual interfaces (cbr0, cni0, etc.)
func (ic *InterfaceClassifier) Classify(attrs *netlink.LinkAttrs) ifaceClass {
	if attrs == nil {
		return classUnknown
	}

	// 1. Loopback interface
	if attrs.Flags&net.FlagLoopback != 0 {
		return classLoopback
	}

	name := attrs.Name
	if name == "" {
		return classUnknown
	}

	// 2. Check if interface should be skipped (internal-only patterns)
	if hasInternalVirtualPrefix(name) {
		ic.logDebug("interface classified as internal virtual skip (name prefix)", name)
		return classInternalVirtualSkip
	}

	// 3. Detect hardware type (virtual or physical)
	isVirtual := ic.isVirtualInterface(name)

	// 4. Check if interface handles external traffic
	isExternal := ic.isExternalInterface(attrs.Index, name)

	// 5. Classify based on combination
	if isExternal {
		if isVirtual {
			ic.logDebug("interface classified as external virtual", name)
			return classExternalVirtual
		}
		ic.logDebug("interface classified as external physical", name)
		return classExternalPhysical
	}

	// Internal virtual interfaces (not caught by name prefix)
	if isVirtual {
		ic.logDebug("interface classified as internal virtual", name)
		return classInternalVirtual
	}

	// Remaining physical interfaces are treated as external by default.
	// Physical NICs are expected to handle outbound traffic even if routes
	// are not yet visible when classification runs.
	ic.logDebug("interface classified as external physical (fallback)", name)
	return classExternalPhysical
}

func (ic *InterfaceClassifier) logDebug(message, iface string) {
	if ic.logger != nil {
		ic.logger.Debug(message, slog.String("interface", iface))
	}
}

// RefreshExternalInterfaces updates the cache of external-facing interfaces.
// Call this before batch classification to improve performance.
func (ic *InterfaceClassifier) RefreshExternalInterfaces() error {
	interval := ic.refreshInterval
	if interval > 0 {
		ic.mu.RLock()
		last := ic.lastRefresh
		ic.mu.RUnlock()
		if !last.IsZero() {
			since := time.Since(last)
			if since < interval {
				if ic.logger != nil {
					ic.logger.Debug("skipping external interface refresh",
						slog.Duration("since_last_refresh", since),
						slog.Duration("refresh_interval", interval))
				}
				return nil
			}
		}
	}

	linkIndexes := make(map[int]struct{})

	fetchRoutes := func(family int, familyLabel string) {
		routes, err := ic.netlinkClient.RouteList(nil, family)
		if err != nil {
			if ic.logger != nil {
				ic.logger.Warn("failed to list routes for external interface detection",
					slog.String("family", familyLabel),
					slog.String("error", err.Error()))
			}
			return
		}

		for _, route := range routes {
			if !isDefaultRoute(route) || route.LinkIndex <= 0 {
				continue
			}

			linkIndexes[route.LinkIndex] = struct{}{}

			if ic.logger != nil {
				if attrs, err := infra.SafeGetLinkAttrs(ic.netlinkClient, route.LinkIndex); err == nil {
					ic.logger.Debug("detected default route for interface",
						slog.String("interface", attrs.Name),
						slog.Int("link_index", route.LinkIndex),
						slog.String("family", familyLabel),
						slog.String("gateway", route.Gw.String()))
				} else {
					ic.logger.Debug("detected default route for link index",
						slog.Int("link_index", route.LinkIndex),
						slog.String("family", familyLabel),
						slog.String("gateway", route.Gw.String()))
				}
			}
		}
	}

	fetchRoutes(netlink.FAMILY_V4, "ipv4")
	fetchRoutes(netlink.FAMILY_V6, "ipv6")

	ic.mu.Lock()
	ic.externalLinkIndexes = linkIndexes
	ic.lastRefresh = time.Now()
	ic.mu.Unlock()

	if ic.logger != nil {
		ic.logger.Info("refreshed external interface cache",
			slog.Int("external_interfaces", len(linkIndexes)),
			slog.Duration("refresh_interval", interval))
	}

	return nil
}

// isExternalInterface checks if an interface handles external traffic.
// An interface is external if:
//  1. It has a default route
//  2. Its name matches external virtual patterns (VPN, tunnels)
//  3. It's cached as external from previous route check
func (ic *InterfaceClassifier) isExternalInterface(linkIndex int, name string) bool {
	// Check explicit external virtual patterns (VPN, tunnels)
	if hasExternalVirtualPrefix(name) {
		return true
	}

	if linkIndex <= 0 {
		return false
	}

	ic.mu.RLock()
	_, ok := ic.externalLinkIndexes[linkIndex]
	ic.mu.RUnlock()
	return ok
}

// isDefaultRoute reports whether the provided route represents a default route (0.0.0.0/0 or ::/0).
func isDefaultRoute(route netlink.Route) bool {
	if route.Dst == nil {
		return true
	}

	ones, bits := route.Dst.Mask.Size()
	return bits > 0 && ones == 0
}

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

// hasInternalVirtualPrefix checks if name matches internal virtual interface patterns.
func hasInternalVirtualPrefix(name string) bool {
	for _, prefix := range internalVirtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// hasExternalVirtualPrefix checks if name matches external virtual interface patterns.
func hasExternalVirtualPrefix(name string) bool {
	for _, prefix := range externalVirtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

package traffic

import (
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
)

// ifaceClass represents the classification of a network interface.
type ifaceClass int

const (
	classUnknown ifaceClass = iota
	classLoopback
	classRoutablePhysical // Physical interface present in routing table
	classRoutableVirtual  // Virtual interface present in routing table
	classSkip             // Interfaces skipped entirely (prefix or not routable)
)

const defaultRoutableRefreshInterval = 30 * time.Second

// InterfaceClassifier provides interface classification with routing awareness.
type InterfaceClassifier struct {
	logger        *slog.Logger
	netlinkClient NetlinkClient

	mu                  sync.RWMutex
	routableLinkIndexes map[int]struct{} // link index -> present in route table
	virtualCache        map[string]bool  // interface name -> is virtual
	lastRefresh         time.Time
	refreshInterval     time.Duration
}

// NewInterfaceClassifier creates a new classifier.
func NewInterfaceClassifier(logger *slog.Logger, netlinkClient NetlinkClient) *InterfaceClassifier {
	return &InterfaceClassifier{
		logger:              logger,
		netlinkClient:       netlinkClient,
		routableLinkIndexes: make(map[int]struct{}),
		virtualCache:        make(map[string]bool),
		refreshInterval:     defaultRoutableRefreshInterval,
	}
}

// Classify determines the class of a network interface.
//
// Classification priority:
//  1. Loopback check (highest priority)
//  2. Skip patterns (ifb, docker, veth, bridge, etc.)
//  3. Routable check (must appear in routing table)
//  4. Virtual/Physical detection (for ethtool differences)
//
// Classification affects which traffic shaping profile is applied:
//   - classLoopback: localhost interface (lo), high MTU and aggressive tuning
//   - classRoutablePhysical: physical NICs in routing table (GRO on)
//   - classRoutableVirtual: virtual interfaces in routing table (GRO off)
//   - classSkip: ignored interfaces (prefix skip or not routable)
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

	// 2. Skip internal-only or managed prefixes early
	if hasSkipPrefix(name) {
		ic.logDebug("interface skipped (prefix match)", name)
		return classSkip
	}

	// 3. Require presence in routing table
	if !ic.isRoutable(attrs.Index, name) {
		ic.logDebug("interface skipped (not in route table)", name)
		return classSkip
	}

	// 4. Hardware detection only influences offload configuration
	if ic.isVirtualInterface(name) {
		ic.logDebug("interface classified as routable virtual", name)
		return classRoutableVirtual
	}

	ic.logDebug("interface classified as routable physical", name)
	return classRoutablePhysical
}

func (ic *InterfaceClassifier) logDebug(message, iface string) {
	if ic.logger != nil {
		ic.logger.Debug(message, slog.String("interface", iface))
	}
}

var (
	// skipPrefixes lists naming patterns that should be ignored entirely.
	skipPrefixes = []string{
		"ifb",    // IFB devices managed separately
		"docker", // Docker bridges
		"veth",   // container veth pairs
		"br",     // generic bridges
		"virbr",  // libvirt bridge
	}

	// virtualPrefixes lists common virtual/tunnel interface prefixes.
	// These are used for ethtool offload differentiation only.
	virtualPrefixes = []string{
		"tun",     // TUN device
		"tap",     // TAP device
		"wg",      // WireGuard VPN
		"zt",      // ZeroTier VPN
		"gre",     // GRE tunnel
		"sit",     // IPv6-in-IPv4 tunnel
		"vxlan",   // VXLAN overlay
		"macvlan", // MAC VLAN
		"ipvlan",  // IP VLAN
	}
)

func hasSkipPrefix(name string) bool {
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func hasVirtualPrefix(name string) bool {
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

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

// RefreshRoutableInterfaces updates the cache of interfaces present in routing tables.
// Call this before batch classification to improve performance.
func (ic *InterfaceClassifier) RefreshRoutableInterfaces() error {
	interval := ic.refreshInterval
	if interval > 0 {
		ic.mu.RLock()
		last := ic.lastRefresh
		ic.mu.RUnlock()
		if !last.IsZero() {
			since := time.Since(last)
			if since < interval {
				if ic.logger != nil {
					ic.logger.Debug("skipping routable interface refresh",
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
				ic.logger.Warn("failed to list routes for routable interface detection",
					slog.String("family", familyLabel),
					slog.String("error", err.Error()))
			}
			return
		}

		for _, route := range routes {
			if route.LinkIndex <= 0 {
				continue
			}

			linkIndexes[route.LinkIndex] = struct{}{}

			if ic.logger != nil {
				if attrs, err := safeGetLinkAttrs(ic.netlinkClient, route.LinkIndex); err == nil {
					ic.logger.Debug("detected route for interface",
						slog.String("interface", attrs.Name),
						slog.Int("link_index", route.LinkIndex),
						slog.String("family", familyLabel))
				} else {
					ic.logger.Debug("detected route for link index",
						slog.Int("link_index", route.LinkIndex),
						slog.String("family", familyLabel))
				}
			}
		}
	}

	fetchRoutes(netlink.FAMILY_V4, "ipv4")
	fetchRoutes(netlink.FAMILY_V6, "ipv6")

	ic.mu.Lock()
	ic.routableLinkIndexes = linkIndexes
	ic.lastRefresh = time.Now()
	ic.mu.Unlock()

	if ic.logger != nil {
		ic.logger.Info("refreshed routable interface cache",
			slog.Int("routable_interfaces", len(linkIndexes)),
			slog.Duration("refresh_interval", interval))
	}

	return nil
}

// isRoutable checks if an interface appears in routing tables.
func (ic *InterfaceClassifier) isRoutable(linkIndex int, _ string) bool {
	if linkIndex <= 0 {
		return false
	}

	ic.mu.RLock()
	_, ok := ic.routableLinkIndexes[linkIndex]
	ic.mu.RUnlock()
	return ok
}

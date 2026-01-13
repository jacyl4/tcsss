package traffic

import (
	"log/slog"
	"net"
	"sync"
	"time"

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

// InterfaceClassifier provides interface classification with routing awareness.
type InterfaceClassifier struct {
	logger        *slog.Logger
	netlinkClient NetlinkClient

	mu                  sync.RWMutex
	externalLinkIndexes map[int]struct{} // link index -> has default route
	virtualCache        map[string]bool  // interface name -> is virtual
	lastRefresh         time.Time
	refreshInterval     time.Duration
}

// NewInterfaceClassifier creates a new classifier.
func NewInterfaceClassifier(logger *slog.Logger, netlinkClient NetlinkClient) *InterfaceClassifier {
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

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

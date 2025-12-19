package traffic

import "strings"

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

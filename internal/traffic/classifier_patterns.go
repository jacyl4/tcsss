package traffic

import "strings"

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

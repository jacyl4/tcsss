package route

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
)

func (opt *Optimizer) getPrimaryNIC() (string, error) {
	if opt.netlink != nil {
		if nic, err := opt.getPrimaryNICFromNetlink(); err == nil && nic != "" {
			return nic, nil
		} else if err != nil && opt.logger != nil {
			opt.logger.Debug("netlink primary NIC detection failed", slog.String("error", err.Error()))
		}
	}
	return opt.getPrimaryNICFromCommand()
}

func (opt *Optimizer) getPrimaryNICFromCommand() (string, error) {
	ctx := context.Background()
	lines, err := opt.fetchRoutes(ctx, "route", "show")
	if err != nil {
		return "", err
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "default ") {
			continue
		}
		if nic, ok := extractDevice(line); ok && nic != "" {
			return nic, nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "linkdown") {
			continue
		}

		nic, ok := extractDevice(line)
		if !ok || nic == "lo" {
			continue
		}
		if !isVirtualName(nic) {
			return nic, nil
		}
	}

	return "", fmt.Errorf("no suitable network interface found")
}

func (opt *Optimizer) getPrimaryNICFromNetlink() (string, error) {
	routes, err := opt.netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("route list: %w", err)
	}

	for _, route := range routes {
		if route.Dst != nil || route.LinkIndex <= 0 {
			continue
		}
		attrs, err := safeGetLinkAttrs(opt.netlink, route.LinkIndex)
		if err != nil {
			continue
		}
		if name := attrs.Name; name != "" {
			return name, nil
		}
	}

	for _, route := range routes {
		if route.LinkIndex <= 0 {
			continue
		}
		attrs, err := safeGetLinkAttrs(opt.netlink, route.LinkIndex)
		if err != nil {
			continue
		}
		name := attrs.Name
		if name == "" || isVirtualName(name) {
			continue
		}
		return name, nil
	}

	return "", fmt.Errorf("no suitable network interface found via netlink")
}

func (opt *Optimizer) getCurrentCongestionControl() (string, error) {
	if output, err := opt.runCommand(nil, "sysctl", "net.ipv4.tcp_congestion_control"); err == nil {
		parts := strings.Split(strings.TrimSpace(output), " = ")
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1]), nil
		}
	}

	data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return "", fmt.Errorf("failed to read congestion control: %w", err)
	}

	if congctl := strings.TrimSpace(string(data)); congctl != "" {
		return congctl, nil
	}

	return "", fmt.Errorf("empty congestion control value")
}

func shouldOptimizeLocal(line string) bool {
	if line == "" || !strings.HasPrefix(line, "local ") {
		return false
	}
	if strings.Contains(line, "broadcast") {
		return false
	}
	if strings.Contains(line, "linkdown") {
		return false
	}
	if device, ok := extractDevice(line); ok && device == "lo" {
		return false
	}
	return true
}

func shouldOptimizeLoopback(line string) bool {
	if line == "" || !strings.HasPrefix(line, "local ") {
		return false
	}
	device, ok := extractDevice(line)
	if !ok {
		return false
	}
	return device == "lo"
}

func shouldOptimizeNIC(line, nic string) bool {
	if line == "" || strings.Contains(line, "linkdown") || strings.Contains(line, "congctl") {
		return false
	}
	device, ok := extractDevice(line)
	if !ok {
		return false
	}
	return device == nic
}

func extractDevice(output string) (string, bool) {
	if output == "" {
		return "", false
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] != "dev" {
				continue
			}
			device := fields[i+1]
			if device == "" {
				return "", false
			}
			return device, true
		}
	}
	return "", false
}

func isVirtualName(name string) bool {
	if name == "" {
		return true
	}
	for _, prefix := range []string{"docker", "br-", "veth", "lo"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

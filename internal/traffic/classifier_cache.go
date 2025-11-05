package traffic

import (
	"log/slog"
	"time"

	"github.com/vishvananda/netlink"
)

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
				if attrs, err := safeGetLinkAttrs(ic.netlinkClient, route.LinkIndex); err == nil {
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

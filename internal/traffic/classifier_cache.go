package traffic

import (
	"log/slog"
	"time"

	"github.com/vishvananda/netlink"
)

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

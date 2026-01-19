package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"

	"tcsss/internal/retry"
)

func (s *Shaper) ensureIfb(ctx context.Context, name, mtu, qlen string) error {
	link, err := s.netlink.LinkByName(name)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			createErr := retry.Do(ctx, retry.Config{MaxAttempts: 3, InitialDelay: 100 * time.Millisecond, MaxDelay: 500 * time.Millisecond}, func() error {
				out, runErr := s.runGetOutput(ctx, "ip", "link", "add", "name", name, "type", "ifb")
				if runErr == nil {
					return nil
				}
				msg := strings.ToLower(out + " " + runErr.Error())
				if strings.Contains(msg, "file exists") {
					return nil
				}
				return runErr
			})
			if createErr != nil {
				return fmt.Errorf("create ifb %s: %w", name, createErr)
			}
			reLookupErr := retry.Do(ctx, retry.Config{MaxAttempts: 3, InitialDelay: 50 * time.Millisecond, MaxDelay: 300 * time.Millisecond}, func() error {
				var lookupErr error
				link, lookupErr = s.netlink.LinkByName(name)
				return lookupErr
			})
			if reLookupErr != nil {
				return fmt.Errorf("lookup ifb %s after create: %w", name, reLookupErr)
			}
		} else {
			return fmt.Errorf("lookup ifb %s: %w", name, err)
		}
	}

	attrs := link.Attrs()
	if attrs == nil {
		return fmt.Errorf("link attrs missing for %s", name)
	}

	desiredMTU, err := strconv.Atoi(mtu)
	if err != nil {
		return fmt.Errorf("parse mtu %q for %s: %w", mtu, name, err)
	}
	desiredQueueLen, err := strconv.Atoi(qlen)
	if err != nil {
		return fmt.Errorf("parse qlen %q for %s: %w", qlen, name, err)
	}

	if attrs.MTU != desiredMTU || attrs.TxQLen != desiredQueueLen {
		if err := s.run(ctx, "ip", "link", "set", name, "qlen", qlen, "mtu", mtu); err != nil {
			return fmt.Errorf("update ifb %s parameters: %w", name, err)
		}
		if refreshed, refreshErr := s.netlink.LinkByName(name); refreshErr == nil && refreshed.Attrs() != nil {
			attrs = refreshed.Attrs()
		}
	}

	if attrs.Flags&net.FlagUp == 0 {
		if err := s.run(ctx, "ip", "link", "set", name, "up"); err != nil {
			return fmt.Errorf("set ifb %s up: %w", name, err)
		}
	}

	return nil
}

// pruneStaleIfbs removes ifb interfaces that do not correspond to any existing base interface.
func (s *Shaper) pruneStaleIfbs(ctx context.Context, links []netlink.Link, requiredIfbs map[string]struct{}) error {
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name
		if strings.HasPrefix(name, IfbPrefix) {
			if _, ok := requiredIfbs[name]; ok {
				continue
			}
			if err := s.netlink.LinkDel(link); err != nil {
				// Try using ip command as fallback and continue
				if runErr := s.runQuiet(ctx, "ip", "link", "del", name); runErr != nil {
					s.logOptional("fallback ifb delete failed", name, runErr, slog.String("command", "ip link del"))
				}
			} else if s.logger != nil {
				s.logger.Debug("pruned stale ifb", slog.String("interface", name))
			}
		}
	}
	return nil
}

func (s *Shaper) cleanupSkippedVirtualInterfaces(ctx context.Context, links []netlink.Link) error {
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name

		// Skip ifb interfaces (handled separately) and loopback
		if strings.HasPrefix(name, "ifb") || attrs.Flags&net.FlagLoopback != 0 {
			continue
		}

		// Only clean up interfaces that match virtual name prefixes (should be skipped)
		if !hasInternalVirtualPrefix(name) {
			continue
		}

		// Remove root qdisc (ignore errors, interface might not have one)
		if err := s.runQuiet(ctx, "tc", "qdisc", "del", "dev", name, "root"); err != nil {
			s.logOptional("skip virtual qdisc root cleanup", name, err, slog.String("command", "tc qdisc del root"))
		}
		// Remove ingress qdisc (ignore errors)
		if err := s.runQuiet(ctx, "tc", "qdisc", "del", "dev", name, "handle", IngressHandle, "ingress"); err != nil {
			s.logOptional("skip virtual ingress qdisc cleanup", name, err, slog.String("command", "tc qdisc del ingress"))
		}

		// Try to remove any associated ifb interface for this interface
		ifbName := truncateIfb(IfbPrefix + name)
		if err := s.runQuiet(ctx, "ip", "link", "del", ifbName); err != nil {
			s.logOptional("skip virtual ifb cleanup", ifbName, err, slog.String("command", "ip link del"))
		}

		if s.logger != nil {
			s.logger.Debug("cleaned up qdisc from skipped virtual interface", slog.String("interface", name))
		}
	}

	return nil
}

func truncateIfb(name string) string {
	if len(name) <= 15 {
		return name
	}
	return name[:15]
}

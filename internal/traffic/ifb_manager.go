package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"

	terr "tcsss/internal/errors"
)

func (s *Shaper) ensureIfb(ctx context.Context, name, mtu, qlen string) error {
	link, err := s.netlink.LinkByName(name)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			if runErr := s.run(ctx, "ip", "link", "add", "name", name, "type", "ifb"); runErr != nil {
				return terr.New(
					terr.CategoryRecoverable,
					fmt.Errorf("create ifb %s: %w", name, runErr),
					terr.ErrorContext{IFB: name, Command: "ip link add"},
				)
			}
			link, err = s.netlink.LinkByName(name)
			if err != nil {
				return terr.New(
					terr.CategoryRecoverable,
					fmt.Errorf("lookup ifb %s after create: %w", name, err),
					terr.ErrorContext{IFB: name, Operation: "link_lookup_post_create"},
				)
			}
		} else {
			return terr.New(
				terr.CategoryRecoverable,
				fmt.Errorf("lookup ifb %s: %w", name, err),
				terr.ErrorContext{IFB: name, Operation: "link_lookup"},
			)
		}
	}

	attrs := link.Attrs()
	if attrs == nil {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("link attrs missing for %s", name),
			terr.ErrorContext{IFB: name},
		)
	}

	desiredMTU, err := strconv.Atoi(mtu)
	if err != nil {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("parse mtu %q for %s: %w", mtu, name, err),
			terr.ErrorContext{IFB: name, Value: mtu},
		)
	}
	desiredQueueLen, err := strconv.Atoi(qlen)
	if err != nil {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("parse qlen %q for %s: %w", qlen, name, err),
			terr.ErrorContext{IFB: name, Value: qlen},
		)
	}

	if attrs.MTU != desiredMTU || attrs.TxQLen != desiredQueueLen {
		if err := s.run(ctx, "ip", "link", "set", name, "qlen", qlen, "mtu", mtu); err != nil {
			return terr.New(
				terr.CategoryRecoverable,
				fmt.Errorf("update ifb %s parameters: %w", name, err),
				terr.ErrorContext{IFB: name, Command: "ip link set"},
			)
		}
		if refreshed, refreshErr := s.netlink.LinkByName(name); refreshErr == nil && refreshed.Attrs() != nil {
			attrs = refreshed.Attrs()
		}
	}

	if attrs.Flags&net.FlagUp == 0 {
		if err := s.run(ctx, "ip", "link", "set", name, "up"); err != nil {
			return terr.New(
				terr.CategoryRecoverable,
				fmt.Errorf("set ifb %s up: %w", name, err),
				terr.ErrorContext{IFB: name, Command: "ip link set up"},
			)
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
					s.logOptional("fallback ifb delete failed", name, runErr, terr.ErrorContext{IFB: name, Command: "ip link del"})
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
			s.logOptional("skip virtual qdisc root cleanup", name, err, terr.ErrorContext{Command: "tc qdisc del root"})
		}
		// Remove ingress qdisc (ignore errors)
		if err := s.runQuiet(ctx, "tc", "qdisc", "del", "dev", name, "handle", IngressHandle, "ingress"); err != nil {
			s.logOptional("skip virtual ingress qdisc cleanup", name, err, terr.ErrorContext{Command: "tc qdisc del ingress"})
		}

		// Try to remove any associated ifb interface for this interface
		ifbName := truncateIfb(IfbPrefix + name)
		if err := s.runQuiet(ctx, "ip", "link", "del", ifbName); err != nil {
			s.logOptional("skip virtual ifb cleanup", ifbName, err, terr.ErrorContext{Command: "ip link del"})
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

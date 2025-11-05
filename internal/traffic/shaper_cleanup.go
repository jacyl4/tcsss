package traffic

import (
	"context"
	"fmt"
	"strings"

	"github.com/vishvananda/netlink"

	terr "tcsss/internal/errors"
)

func (s *Shaper) ensureInitialCleanup(ctx context.Context, links []netlink.Link) {
	if s.didInitialCleanup {
		return
	}
	if err := s.cleanupSkippedVirtualInterfaces(ctx, links); err != nil {
		s.handleCategorizedError("cleanup skipped virtual interfaces failed", "", err, terr.CategoryRecoverable)
	}
	s.didInitialCleanup = true
}

func (s *Shaper) determineRequiredIfbs(links []netlink.Link) map[string]struct{} {
	required := map[string]struct{}{}
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name
		if name == "" || strings.HasPrefix(name, "ifb") {
			continue
		}
		class := s.classifier.Classify(attrs)
		switch class {
		case classLoopback, classExternalPhysical, classExternalVirtual, classInternalVirtual:
			// These classes need IFB devices for ingress shaping
			required[truncateIfb(IfbPrefix+name)] = struct{}{}
		case classInternalVirtualSkip:
			// Internal virtual interfaces with skip prefixes are ignored
			continue
		}
	}
	return required
}

func (s *Shaper) cleanupStaleSignatures() error {
	links, err := s.netlink.LinkList()
	if err != nil {
		return terr.New(
			terr.CategoryRecoverable,
			fmt.Errorf("list links for signature cleanup: %w", err),
			terr.ErrorContext{Operation: "link_list_cleanup"},
		)
	}

	current := make(map[string]struct{}, len(links))
	for _, link := range links {
		if attrs := link.Attrs(); attrs != nil && attrs.Name != "" {
			current[attrs.Name] = struct{}{}
		}
	}

	s.appliedMu.Lock()
	for name := range s.appliedSignatures {
		if _, exists := current[name]; !exists {
			delete(s.appliedSignatures, name)
		}
	}
	s.appliedMu.Unlock()

	return nil
}

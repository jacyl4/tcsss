package traffic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// IfbPrefix is the prefix used for IFB interface names. tc limits names to 15 chars.
	IfbPrefix = "ifb4"
	// IngressHandle is the tc handle identifier reserved for ingress qdiscs.
	IngressHandle = "ffff:"
	// defaultWorkerCount limits concurrent interface configuration to a small, safe pool.
	defaultWorkerCount = 4
)

// isContextError reports whether an error is caused by context cancellation or deadline.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// makeSignature creates a lightweight signature describing desired state to avoid redundant tc/ethtool calls
func (s *Shaper) makeSignature(mtu, qlen string, profile shapingProfile) string {
	var b strings.Builder
	b.WriteString("mtu=")
	b.WriteString(mtu)
	b.WriteString(";qlen=")
	b.WriteString(qlen)
	b.WriteString(";root=")
	b.WriteString(strings.Join(profile.rootQdisc, ","))
	b.WriteString(";ifb=")
	b.WriteString(strings.Join(profile.ifbQdisc, ","))
	b.WriteString(";off=")
	// Sort offloads for stable signature
	if len(profile.offloads) > 0 {
		pairs := make([]string, 0, len(profile.offloads))
		for _, o := range profile.offloads {
			pairs = append(pairs, fmt.Sprintf("%s=%s", normalizeSetFeatureName(o.feature), strings.ToLower(o.state)))
		}
		// No guarantee of order in source; sort
		sort.Strings(pairs)
		b.WriteString(strings.Join(pairs, ","))
	}
	return b.String()
}

package traffic

import (
	"fmt"
	"sort"
	"strings"
)

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

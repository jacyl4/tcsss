package traffic

import (
	"context"
	"strings"

	terr "tcsss/internal/errors"
)

var suppressOffloads = []string{
	"Operation not supported",
	"bit name not found",
	"cannot modify an unsupported parameter",
}

// ensureOffloads minimizes ethtool calls by only changing mismatched settings, batching into a single -K call
func (s *Shaper) ensureOffloads(ctx context.Context, iface string, settings []offloadSetting) {
	if len(settings) == 0 {
		return
	}

	cur, fixed := s.readEthtoolFeatures(ctx, iface)
	if cur == nil {
		// Fallback: best-effort single calls
		for _, setting := range settings {
			feat := normalizeSetFeatureName(setting.feature)
			args := []string{"-K", iface, feat, setting.state}
			if err := s.runOptional(ctx, "ethtool", args, suppressOffloads); err != nil {
				s.logOptional("ethtool feature apply skipped", iface, err, terr.ErrorContext{
					Command: "ethtool -K",
					Extra: map[string]any{
						"feature": feat,
						"state":   setting.state,
					},
				})
			}
		}
		return
	}

	var batched []string
	for _, s := range settings {
		readKey := mapDesiredToReadKey(s.feature)
		setKey := normalizeSetFeatureName(s.feature)
		if readKey == "" || setKey == "" {
			continue
		}
		if fixed[readKey] {
			continue
		}
		if curState, ok := cur[readKey]; ok && strings.EqualFold(curState, s.state) {
			continue
		}
		batched = append(batched, setKey, s.state)
	}

	if len(batched) == 0 {
		return
	}

	args := append([]string{"-K", iface}, batched...)
	if err := s.runOptional(ctx, "ethtool", args, suppressOffloads); err != nil {
		s.logOptional("batched ethtool features skipped", iface, err, terr.ErrorContext{
			Command: "ethtool -K",
			Extra: map[string]any{
				"features": batched,
			},
		})
	}
}

// readEthtoolFeatures runs 'ethtool -k' and parses feature states and fixed flags
func (s *Shaper) readEthtoolFeatures(ctx context.Context, iface string) (map[string]string, map[string]bool) {
	out, err := s.runGetOutput(ctx, "ethtool", "-k", iface)
	if err != nil || out == "" {
		return nil, nil
	}
	features := map[string]string{}
	fixed := map[string]bool{}
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// Skip header lines
		if strings.HasPrefix(strings.ToLower(ln), "features for ") || strings.HasPrefix(strings.ToLower(ln), "offload parameters for ") {
			continue
		}
		parts := strings.SplitN(ln, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		vstate := ""
		if strings.Contains(strings.ToLower(val), "on") {
			vstate = "on"
		} else if strings.Contains(strings.ToLower(val), "off") {
			vstate = "off"
		}
		features[key] = vstate
		if strings.Contains(strings.ToLower(val), "[fixed]") {
			fixed[key] = true
		}
	}
	return features, fixed
}

// normalizeSetFeatureName maps various aliases to the canonical ethtool -K feature name
func normalizeSetFeatureName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "rx-checksum", "rx_checksum":
		return "rx"
	case "tx-checksum", "tx_checksum":
		return "tx"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// mapDesiredToReadKey maps desired feature names to ethtool -k output keys
func mapDesiredToReadKey(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "rx", "rx-checksum", "rx_checksum":
		return "rx-checksumming"
	case "tx", "tx-checksum", "tx_checksum":
		return "tx-checksumming"
	case "sg", "scatter-gather":
		return "scatter-gather"
	case "tso":
		return "tcp-segmentation-offload"
	case "gso":
		return "generic-segmentation-offload"
	case "gro":
		return "generic-receive-offload"
	case "lro":
		return "large-receive-offload"
	case "ufo":
		return "udp-fragmentation-offload"
	case "tx-gso-partial":
		return "tx-gso-partial"
	case "tx-scatter-gather":
		return "tx-scatter-gather"
	default:
		return ""
	}
}

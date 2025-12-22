package traffic

import (
	"context"
	"strings"
	"time"

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
	desiredStates := make(map[string]string)
	for _, setting := range settings {
		readKey := mapDesiredToReadKey(setting.feature)
		setKey := normalizeSetFeatureName(setting.feature)
		if readKey == "" || setKey == "" {
			continue
		}
		if fixed[readKey] {
			continue
		}
		desiredState := strings.ToLower(setting.state)
		if curState, ok := cur[readKey]; ok && strings.EqualFold(curState, desiredState) {
			continue
		}
		batched = append(batched, setKey, desiredState)
		desiredStates[readKey] = desiredState
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
		s.invalidateEthtoolCache(iface)
		return
	}

	if len(desiredStates) > 0 {
		for key, state := range desiredStates {
			cur[key] = state
		}
		s.storeEthtoolCache(iface, cur, fixed)
	}
}

// readEthtoolFeatures runs 'ethtool -k' and parses feature states and fixed flags
func (s *Shaper) readEthtoolFeatures(ctx context.Context, iface string) (map[string]string, map[string]bool) {
	features, fixed := s.loadEthtoolCache(iface)
	if features != nil || fixed != nil {
		return features, fixed
	}

	out, err := s.runGetOutput(ctx, "ethtool", "-k", iface)
	if err != nil || out == "" {
		return nil, nil
	}

	features, fixed = parseEthtoolFeatures(out)
	if len(features) == 0 && len(fixed) == 0 {
		return nil, nil
	}

	s.storeEthtoolCache(iface, features, fixed)
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

func parseEthtoolFeatures(out string) (map[string]string, map[string]bool) {
	features := map[string]string{}
	fixed := map[string]bool{}

	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		lower := strings.ToLower(ln)
		if strings.HasPrefix(lower, "features for ") || strings.HasPrefix(lower, "offload parameters for ") {
			continue
		}
		parts := strings.SplitN(ln, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		state := ""
		valLower := strings.ToLower(val)
		if strings.Contains(valLower, "on") {
			state = "on"
		} else if strings.Contains(valLower, "off") {
			state = "off"
		}
		if key != "" {
			features[key] = state
			if strings.Contains(valLower, "[fixed]") {
				fixed[key] = true
			}
		}
	}

	return features, fixed
}

type ethtoolCacheEntry struct {
	features  map[string]string
	fixed     map[string]bool
	expiresAt time.Time
}

func (s *Shaper) loadEthtoolCache(iface string) (map[string]string, map[string]bool) {
	if s == nil || iface == "" || s.ethtoolCacheTTL <= 0 {
		return nil, nil
	}

	s.ethtoolCacheMu.RLock()
	entry := s.ethtoolCache[iface]
	s.ethtoolCacheMu.RUnlock()

	if entry == nil {
		return nil, nil
	}

	if time.Now().After(entry.expiresAt) {
		s.invalidateEthtoolCache(iface)
		return nil, nil
	}

	return cloneStringMap(entry.features), cloneBoolMap(entry.fixed)
}

func (s *Shaper) storeEthtoolCache(iface string, features map[string]string, fixed map[string]bool) {
	if s == nil || iface == "" || s.ethtoolCacheTTL <= 0 {
		return
	}

	entry := &ethtoolCacheEntry{
		features:  cloneStringMap(features),
		fixed:     cloneBoolMap(fixed),
		expiresAt: time.Now().Add(s.ethtoolCacheTTL),
	}

	s.ethtoolCacheMu.Lock()
	s.ethtoolCache[iface] = entry
	s.ethtoolCacheMu.Unlock()
}

func (s *Shaper) invalidateEthtoolCache(iface string) {
	if s == nil || iface == "" {
		return
	}
	s.ethtoolCacheMu.Lock()
	delete(s.ethtoolCache, iface)
	s.ethtoolCacheMu.Unlock()
}

func (s *Shaper) invalidateEthtoolCacheAll() {
	if s == nil {
		return
	}
	s.ethtoolCacheMu.Lock()
	s.ethtoolCache = make(map[string]*ethtoolCacheEntry)
	s.ethtoolCacheMu.Unlock()
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return map[string]bool{}
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

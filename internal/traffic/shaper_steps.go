package traffic

import (
	"context"
	"fmt"

	terr "tcsss/internal/errors"
)

func (s *Shaper) configureLinkParamsStep(ctx context.Context, pc *profileContext) error {
	if pc.attrs.MTU == pc.desiredMTU && pc.attrs.TxQLen == pc.desiredQueueLen {
		return nil
	}
	link, err := s.netlink.LinkByName(pc.iface)
	if err != nil {
		return terr.WrapRecoverable(
			fmt.Errorf("lookup link %s: %w", pc.iface, err),
			"configure_link_params",
			terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName},
		)
	}

	if pc.attrs.MTU != pc.desiredMTU {
		if err := s.netlink.LinkSetMTU(link, pc.desiredMTU); err != nil {
			return terr.WrapRecoverable(
				fmt.Errorf("set mtu %d for %s: %w", pc.desiredMTU, pc.iface, err),
				"configure_link_params",
				terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName, Value: pc.mtuStr},
			)
		}
	}

	if pc.attrs.TxQLen != pc.desiredQueueLen {
		if err := s.netlink.LinkSetTxQLen(link, pc.desiredQueueLen); err != nil {
			return terr.WrapRecoverable(
				fmt.Errorf("set tx queue len %d for %s: %w", pc.desiredQueueLen, pc.iface, err),
				"configure_link_params",
				terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName, Value: pc.queueLength},
			)
		}
	}
	return nil
}

func (s *Shaper) configureRootQdiscStep(ctx context.Context, pc *profileContext) error {
	if len(pc.profile.rootQdisc) == 0 {
		return nil
	}
	qdisc := rootQdiscConfig(pc.iface, pc.profile.rootQdisc)
	if err := s.run(ctx, "tc", qdisc.ReplaceArgs()...); err != nil {
		return terr.WrapRecoverable(
			fmt.Errorf("configure root qdisc for %s: %w", pc.iface, err),
			"configure_root_qdisc",
			terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName, Command: "tc qdisc replace root"},
		)
	}
	return nil
}

func (s *Shaper) configureIngressAndIfbStep(ctx context.Context, pc *profileContext) error {
	ingress := ingressQdiscConfig(pc.iface)
	if err := s.ensureIfb(ctx, pc.ifbName, pc.mtuStr, pc.queueLength); err != nil {
		return terr.WrapRecoverable(
			fmt.Errorf("ensure ifb %s for %s: %w", pc.ifbName, pc.iface, err),
			"ensure_ifb",
			terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName, IFB: pc.ifbName},
		)
	}

	var tcBatch [][]string
	tcBatch = append(tcBatch, ingress.ReplaceArgs())

	if len(pc.profile.ifbQdisc) > 0 {
		ifbRoot := ifbRootQdiscConfig(pc.ifbName, pc.profile.ifbQdisc)
		tcBatch = append(tcBatch, ifbRoot.ReplaceArgs())
	}

	filter := FilterConfig{
		Device:   pc.iface,
		Parent:   IngressHandle,
		Protocol: "all",
		Pref:     "1",
		Kind:     "matchall",
		Actions:  []string{"action", "mirred", "egress", "redirect", "dev", pc.ifbName},
	}
	_ = s.runQuiet(ctx, "tc", filter.DeleteArgs()...)
	tcBatch = append(tcBatch, filter.AddArgs())

	if err := s.runTcBatch(ctx, tcBatch); err != nil {
		return terr.WrapRecoverable(
			fmt.Errorf("configure ingress pipeline for %s -> %s: %w", pc.iface, pc.ifbName, err),
			"configure_tc_filter",
			terr.ErrorContext{Interface: pc.iface, Profile: pc.profileName, IFB: pc.ifbName, Command: "tc filter replace"},
		)
	}

	return nil
}

func (s *Shaper) ensureOffloadsStep(ctx context.Context, pc *profileContext) error {
	s.ensureOffloads(ctx, pc.iface, pc.profile.offloads)
	return nil
}

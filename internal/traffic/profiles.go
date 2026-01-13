package traffic

import (
	"strconv"
	"strings"
	"time"
)

type offloadSetting struct {
	feature string
	state   string
}

type shapingProfile struct {
	queueLength string
	rootQdisc   []string
	ifbQdisc    []string
	offloads    []offloadSetting
	mtuOverride string
}

type profileSet struct {
	internalVirtual  shapingProfile
	externalVirtual  shapingProfile
	externalPhysical shapingProfile
	loopback         shapingProfile
}

var (
	offloadPrefix = []offloadSetting{
		{"rx", "on"},
		{"tx", "on"},
		{"sg", "off"},
		{"tso", "off"},
		{"gso", "off"},
	}

	offloadSuffix = []offloadSetting{
		{"lro", "off"},
		{"ufo", "off"},
		{"rx-checksum", "on"},
		{"tx-checksum", "on"},
		{"tx-scatter-gather", "off"},
		{"tx-gso-partial", "off"},
	}

	suppressLinkSettings = []string{
		"Operation not supported",
		"cannot modify an unsupported parameter",
	}
)

func newProfileSet(cfg ProfileSettings) profileSet {
	queue := strconv.Itoa(cfg.DefaultQueueLen)
	loopbackQueue := strconv.Itoa(cfg.LoopbackQueueLen)
	internalRTT := renderDuration(cfg.InternalRTT)
	loopbackRTT := renderDuration(cfg.LoopbackRTT)
	loopbackMTUOverride := strconv.Itoa(cfg.LoopbackMTUOverride)

	internalRootQdisc := []string{
		"cake", "unlimited", "rtt", internalRTT, "besteffort", "dual-srchost",
		"nonat", "nowash", "no-split-gso", "ack-filter", "raw", "egress",
	}

	internalIfbQdisc := []string{
		"cake", "unlimited", "rtt", internalRTT, "diffserv4", "dual-dsthost",
		"nonat", "nowash", "no-split-gso", "no-ack-filter", "raw", "ingress",
	}

	externalRootQdisc := []string{
		"cake", "unlimited", "besteffort", "dual-srchost", "nonat",
		"nowash", "no-split-gso", "ack-filter", "ethernet", "egress",
	}

	externalIfbQdisc := []string{
		"cake", "unlimited", "diffserv4", "dual-dsthost", "nonat",
		"nowash", "no-split-gso", "no-ack-filter", "ethernet", "ingress",
	}

	loopbackRootQdisc := []string{
		"cake", "unlimited", "rtt", loopbackRTT, "diffserv4", "dual-srchost",
		"nonat", "nowash", "no-split-gso", "ack-filter-aggressive", "raw", "egress",
	}

	loopbackIfbQdisc := []string{
		"cake", "unlimited", "rtt", loopbackRTT, "diffserv4", "dual-dsthost",
		"nonat", "nowash", "no-split-gso", "no-ack-filter", "raw", "ingress",
	}

	return profileSet{
		internalVirtual: shapingProfile{
			queueLength: queue,
			rootQdisc:   internalRootQdisc,
			ifbQdisc:    internalIfbQdisc,
			offloads:    offloadsWithGro("off"),
		},
		externalVirtual: shapingProfile{
			queueLength: queue,
			rootQdisc:   externalRootQdisc,
			ifbQdisc:    externalIfbQdisc,
			offloads:    offloadsWithGro("off"),
		},
		externalPhysical: shapingProfile{
			queueLength: queue,
			rootQdisc:   externalRootQdisc,
			ifbQdisc:    externalIfbQdisc,
			offloads:    offloadsWithGro("on"),
		},
		loopback: shapingProfile{
			queueLength: loopbackQueue,
			rootQdisc:   loopbackRootQdisc,
			ifbQdisc:    loopbackIfbQdisc,
			offloads:    offloadsWithGro("off"),
			mtuOverride: loopbackMTUOverride,
		},
	}
}

func offloadsWithGro(state string) []offloadSetting {
	result := make([]offloadSetting, 0, len(offloadPrefix)+1+len(offloadSuffix))
	result = append(result, offloadPrefix...)
	result = append(result, offloadSetting{"gro", state})
	result = append(result, offloadSuffix...)
	return result
}

func renderDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	s := d.String()
	// Ensure ASCII-only output.
	s = strings.ReplaceAll(s, "µs", "us")
	return s
}

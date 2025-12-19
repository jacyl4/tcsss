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
	routablePhysical shapingProfile
	routableVirtual  shapingProfile
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
	loopbackRTT := renderDuration(cfg.LoopbackRTT)
	loopbackMTUOverride := strconv.Itoa(cfg.LoopbackMTUOverride)

	routableRootQdisc := []string{
		"cake", "unlimited", "besteffort", "dual-srchost", "nonat",
		"nowash", "no-split-gso", "ack-filter", "ethernet", "egress",
	}

	routableIfbQdisc := []string{
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
		routablePhysical: shapingProfile{
			queueLength: queue,
			rootQdisc:   routableRootQdisc,
			ifbQdisc:    routableIfbQdisc,
			offloads:    offloadsWithGro("on"),
		},
		routableVirtual: shapingProfile{
			queueLength: queue,
			rootQdisc:   routableRootQdisc,
			ifbQdisc:    routableIfbQdisc,
			offloads:    offloadsWithGro("off"),
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

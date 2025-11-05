package traffic

import (
	"time"

	route "tcsss/internal/route"
)

// WatcherSettings controls cadence of the netlink watcher.
type WatcherSettings struct {
	ReapplyInterval time.Duration
	CleanupInterval time.Duration
	ApplyTimeout    time.Duration
}

// ProfileSettings customises shaping profile parameters.
type ProfileSettings struct {
	DefaultQueueLen     int
	LoopbackQueueLen    int
	LoopbackMTUOverride int
	InternalRTT         time.Duration
	LoopbackRTT         time.Duration
}

// Settings encapsulates the inputs required to build a Shaper.
type Settings struct {
	Routes   route.WindowConfig
	Watcher  WatcherSettings
	Profiles ProfileSettings
}

const (
	defaultApplyTimeout    = 45 * time.Second
	defaultReapplyInterval = 2 * time.Second
	defaultCleanupInterval = 5 * time.Minute
	defaultQueueLen        = 10001
	defaultLoopbackQueue   = 10000
	defaultLoopbackMTU     = 65520
	defaultInternalRTT     = 100 * time.Microsecond
	defaultLoopbackRTT     = 20 * time.Microsecond
)

func (s Settings) withDefaults() Settings {
	s.Routes = s.Routes.WithDefaults()
	if s.Watcher.ReapplyInterval <= 0 {
		s.Watcher.ReapplyInterval = defaultReapplyInterval
	}
	if s.Watcher.CleanupInterval <= 0 {
		s.Watcher.CleanupInterval = defaultCleanupInterval
	}
	if s.Watcher.ApplyTimeout <= 0 {
		s.Watcher.ApplyTimeout = defaultApplyTimeout
	}

	if s.Profiles.DefaultQueueLen <= 0 {
		s.Profiles.DefaultQueueLen = defaultQueueLen
	}
	if s.Profiles.LoopbackQueueLen <= 0 {
		s.Profiles.LoopbackQueueLen = defaultLoopbackQueue
	}
	if s.Profiles.LoopbackMTUOverride <= 0 {
		s.Profiles.LoopbackMTUOverride = defaultLoopbackMTU
	}
	if s.Profiles.InternalRTT <= 0 {
		s.Profiles.InternalRTT = defaultInternalRTT
	}
	if s.Profiles.LoopbackRTT <= 0 {
		s.Profiles.LoopbackRTT = defaultLoopbackRTT
	}
	return s
}

package traffic

import (
	"time"

	"tcsss/internal/config"
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

func (s Settings) withDefaults() Settings {
	s.Routes = s.Routes.WithDefaults()
	if s.Watcher.ReapplyInterval <= 0 {
		s.Watcher.ReapplyInterval = config.DefaultWatcherReapplyInterval
	}
	if s.Watcher.CleanupInterval <= 0 {
		s.Watcher.CleanupInterval = config.DefaultWatcherCleanupInterval
	}
	if s.Watcher.ApplyTimeout <= 0 {
		s.Watcher.ApplyTimeout = config.DefaultWatcherApplyTimeout
	}

	if s.Profiles.DefaultQueueLen <= 0 {
		s.Profiles.DefaultQueueLen = config.DefaultTxQueueLen
	}
	if s.Profiles.LoopbackQueueLen <= 0 {
		s.Profiles.LoopbackQueueLen = config.DefaultLoopbackTxQueueLen
	}
	if s.Profiles.LoopbackMTUOverride <= 0 {
		s.Profiles.LoopbackMTUOverride = config.DefaultLoopbackMSS
	}
	if s.Profiles.InternalRTT <= 0 {
		s.Profiles.InternalRTT = config.DefaultInternalRTT
	}
	if s.Profiles.LoopbackRTT <= 0 {
		s.Profiles.LoopbackRTT = config.DefaultLoopbackRTT
	}
	return s
}

package config

import (
	"fmt"
	"time"
)

const (
	defaultStandardMTU        = 1500
	defaultStandardMSS        = 1460
	defaultLoopbackMTU        = 65536
	defaultLoopbackMSS        = 65520
	defaultTxQueueLen         = 10001
	defaultLoopbackTxQueueLen = 10000
	defaultInternalRTT        = 100 * time.Microsecond
	defaultLoopbackRTT        = 20 * time.Microsecond

	defaultInitCwndBytes       = 146000
	defaultInitRwndBytes       = 146000
	defaultLoopbackWindowBytes = 16 * 1024 * 1024 // 16 MiB

	defaultWatcherReapplyInterval = 2 * time.Second
	defaultWatcherCleanupInterval = 5 * time.Minute
	defaultWatcherApplyTimeout    = 45 * time.Second
)

// Config represents the top-level tcsss configuration.
type Config struct {
	Network NetworkConfig `yaml:"network" json:"network"`
	Traffic TrafficConfig `yaml:"traffic" json:"traffic"`
}

// NetworkConfig controls MTU, MSS and queue-length defaults.
type NetworkConfig struct {
	StandardMTU        int           `yaml:"standard_mtu" json:"standard_mtu"`
	StandardMSS        int           `yaml:"standard_mss" json:"standard_mss"`
	LoopbackMTU        int           `yaml:"loopback_mtu" json:"loopback_mtu"`
	LoopbackMSS        int           `yaml:"loopback_mss" json:"loopback_mss"`
	DefaultTxQueueLen  int           `yaml:"default_tx_queue_len" json:"default_tx_queue_len"`
	LoopbackTxQueueLen int           `yaml:"loopback_tx_queue_len" json:"loopback_tx_queue_len"`
	InternalRTT        time.Duration `yaml:"internal_rtt" json:"internal_rtt"`
	LoopbackRTT        time.Duration `yaml:"loopback_rtt" json:"loopback_rtt"`
}

// TrafficConfig controls traffic shaping behaviour.
type TrafficConfig struct {
	Routes  RouteConfig   `yaml:"routes" json:"routes"`
	Watcher WatcherConfig `yaml:"watcher" json:"watcher"`
}

// RouteConfig defines TCP window tuning defaults.
type RouteConfig struct {
	MSSBytes            int `yaml:"mss_bytes" json:"mss_bytes"`
	InitCwndBytes       int `yaml:"init_cwnd_bytes" json:"init_cwnd_bytes"`
	InitRwndBytes       int `yaml:"init_rwnd_bytes" json:"init_rwnd_bytes"`
	LoopbackWindowBytes int `yaml:"loopback_window_bytes" json:"loopback_window_bytes"`
}

// WatcherConfig defines netlink watcher intervals.
type WatcherConfig struct {
	ReapplyInterval time.Duration `yaml:"reapply_interval" json:"reapply_interval"`
	CleanupInterval time.Duration `yaml:"cleanup_interval" json:"cleanup_interval"`
	ApplyTimeout    time.Duration `yaml:"apply_timeout" json:"apply_timeout"`
}

// Default returns Config populated with recommended defaults.
func Default() Config {
	return Config{
		Network: NetworkConfig{
			StandardMTU:        defaultStandardMTU,
			StandardMSS:        defaultStandardMSS,
			LoopbackMTU:        defaultLoopbackMTU,
			LoopbackMSS:        defaultLoopbackMSS,
			DefaultTxQueueLen:  defaultTxQueueLen,
			LoopbackTxQueueLen: defaultLoopbackTxQueueLen,
			InternalRTT:        defaultInternalRTT,
			LoopbackRTT:        defaultLoopbackRTT,
		},
		Traffic: TrafficConfig{
			Routes: RouteConfig{
				MSSBytes:            defaultStandardMSS,
				InitCwndBytes:       defaultInitCwndBytes,
				InitRwndBytes:       defaultInitRwndBytes,
				LoopbackWindowBytes: defaultLoopbackWindowBytes,
			},
			Watcher: WatcherConfig{
				ReapplyInterval: defaultWatcherReapplyInterval,
				CleanupInterval: defaultWatcherCleanupInterval,
				ApplyTimeout:    defaultWatcherApplyTimeout,
			},
		},
	}
}

// ApplyDefaults normalises missing or zero values.
func (c *Config) ApplyDefaults() {
	if c == nil {
		return
	}
	if c.Network.StandardMTU <= 0 {
		c.Network.StandardMTU = defaultStandardMTU
	}
	if c.Network.StandardMSS <= 0 {
		c.Network.StandardMSS = defaultStandardMSS
	}
	if c.Network.LoopbackMTU <= 0 {
		c.Network.LoopbackMTU = defaultLoopbackMTU
	}
	if c.Network.LoopbackMSS <= 0 {
		c.Network.LoopbackMSS = defaultLoopbackMSS
	}
	if c.Network.DefaultTxQueueLen <= 0 {
		c.Network.DefaultTxQueueLen = defaultTxQueueLen
	}
	if c.Network.LoopbackTxQueueLen <= 0 {
		c.Network.LoopbackTxQueueLen = defaultLoopbackTxQueueLen
	}
	if c.Network.InternalRTT <= 0 {
		c.Network.InternalRTT = defaultInternalRTT
	}
	if c.Network.LoopbackRTT <= 0 {
		c.Network.LoopbackRTT = defaultLoopbackRTT
	}

	if c.Traffic.Routes.MSSBytes <= 0 {
		c.Traffic.Routes.MSSBytes = defaultStandardMSS
	}
	if c.Traffic.Routes.InitCwndBytes <= 0 {
		c.Traffic.Routes.InitCwndBytes = defaultInitCwndBytes
	}
	if c.Traffic.Routes.InitRwndBytes <= 0 {
		c.Traffic.Routes.InitRwndBytes = defaultInitRwndBytes
	}
	if c.Traffic.Routes.LoopbackWindowBytes <= 0 {
		c.Traffic.Routes.LoopbackWindowBytes = defaultLoopbackWindowBytes
	}

	if c.Traffic.Watcher.ReapplyInterval <= 0 {
		c.Traffic.Watcher.ReapplyInterval = defaultWatcherReapplyInterval
	}
	if c.Traffic.Watcher.CleanupInterval <= 0 {
		c.Traffic.Watcher.CleanupInterval = defaultWatcherCleanupInterval
	}
	if c.Traffic.Watcher.ApplyTimeout <= 0 {
		c.Traffic.Watcher.ApplyTimeout = defaultWatcherApplyTimeout
	}
}

// Validate performs boundary checks and returns the first error encountered.
func (c Config) Validate() error {
	if c.Network.StandardMTU <= 0 {
		return fmt.Errorf("network.standard_mtu must be positive")
	}
	if c.Network.StandardMSS <= 0 || c.Network.StandardMSS >= c.Network.StandardMTU {
		return fmt.Errorf("network.standard_mss must be positive and less than MTU")
	}
	if c.Network.LoopbackMTU <= 0 || c.Network.LoopbackMSS <= 0 {
		return fmt.Errorf("network.loopback mtu/mss must be positive")
	}
	if c.Network.DefaultTxQueueLen <= 0 || c.Network.LoopbackTxQueueLen <= 0 {
		return fmt.Errorf("network.tx_queue_len values must be positive")
	}
	if c.Traffic.Routes.MSSBytes <= 0 {
		return fmt.Errorf("traffic.routes.mss_bytes must be positive")
	}
	if c.Traffic.Routes.InitCwndBytes <= 0 || c.Traffic.Routes.InitRwndBytes <= 0 {
		return fmt.Errorf("traffic.routes init window sizes must be positive")
	}
	if c.Traffic.Watcher.ReapplyInterval <= 0 {
		return fmt.Errorf("traffic.watcher.reapply_interval must be positive")
	}
	if c.Traffic.Watcher.CleanupInterval <= 0 {
		return fmt.Errorf("traffic.watcher.cleanup_interval must be positive")
	}
	if c.Traffic.Watcher.ApplyTimeout <= 0 {
		return fmt.Errorf("traffic.watcher.apply_timeout must be positive")
	}
	return nil
}

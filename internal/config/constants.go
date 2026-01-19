package config

import "time"

const (
	// MemoryEffectivenessFactor specifies the portion of total memory considered safe for tuning decisions.
	MemoryEffectivenessFactor = 0.8

	// MaximumSupportedMemoryMB guards against unrealistic detection results (~100 TB).
	MaximumSupportedMemoryMB = 1024 * 1024 * 100

	// MinMTU and MaxMTU define acceptable MTU boundaries (RFC 791).
	MinMTU = 68
	MaxMTU = 65535

	// MinQueueLen and MaxQueueLen bound queue length settings passed to tc.
	MinQueueLen = 1
	MaxQueueLen = 1_000_000

	// Default network configuration values.
	DefaultLoopbackMSS        = 65520
	DefaultTxQueueLen         = 10001
	DefaultLoopbackTxQueueLen = 10000
	DefaultInternalRTT        = 100 * time.Microsecond
	DefaultLoopbackRTT        = 20 * time.Microsecond

	// Default route tuning values.
	DefaultInitCwndBytes       = 146000
	DefaultInitRwndBytes       = 146000
	DefaultLoopbackWindowBytes = 16 * 1024 * 1024 // 16 MiB

	// Default watcher intervals reused across the daemon.
	DefaultWatcherReapplyInterval = 2 * time.Second
	DefaultWatcherCleanupInterval = 5 * time.Minute
	DefaultWatcherApplyTimeout    = 45 * time.Second

	// DefaultChannelBuffer standardises buffered channel sizes across workers.
	DefaultChannelBuffer = 32
)

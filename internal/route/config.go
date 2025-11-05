package route

import (
	"strconv"
	"time"
)

// WindowConfig defines the TCP window byte sizes used during route optimization.
type WindowConfig struct {
	MSSBytes            int
	InitCwndBytes       int
	InitRwndBytes       int
	LoopbackWindowBytes int
}

const (
	defaultMSS        = 1460
	defaultCmdTimeout = 5 * time.Second
)

type params struct {
	mtu      int
	initCwnd int
	initRwnd int
	congctl  string
}

func newParams(mtu, initCwnd, initRwnd int, congctl string) params {
	return params{
		mtu:      mtu,
		initCwnd: initCwnd,
		initRwnd: initRwnd,
		congctl:  congctl,
	}
}

func (p params) args() []string {
	result := []string{
		"mtu", strconv.Itoa(p.mtu),
		"initcwnd", strconv.Itoa(p.initCwnd),
		"initrwnd", strconv.Itoa(p.initRwnd),
		"fastopen_no_cookie", "1",
	}
	if p.congctl != "" {
		result = append(result, "congctl", "lock", p.congctl)
	}
	return result
}

func (cfg WindowConfig) WithDefaults() WindowConfig {
	if cfg.MSSBytes <= 0 {
		cfg.MSSBytes = defaultMSS
	}
	return cfg
}

func bytesToSegments(bytes, mss int) int {
	if mss <= 0 || bytes <= 0 {
		return 0
	}
	return (bytes + mss - 1) / mss
}

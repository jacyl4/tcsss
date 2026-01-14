package retry

import (
	"context"
	"fmt"
	"time"
)

type Config struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	RetryIf       func(error) bool
}

func (c Config) withDefaults() Config {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.InitialDelay <= 0 {
		c.InitialDelay = 100 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 5 * time.Second
	}
	if c.BackoffFactor <= 1 {
		c.BackoffFactor = 2
	}
	return c
}

// Do executes fn with retries and exponential backoff until it succeeds, the context is cancelled, or attempts are exhausted.
func Do(ctx context.Context, cfg Config, fn func() error) error {
	cfg = cfg.withDefaults()
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
			if cfg.RetryIf != nil && !cfg.RetryIf(err) {
				return err
			}
		}

		if attempt == cfg.MaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		next := time.Duration(float64(delay) * cfg.BackoffFactor)
		if next > cfg.MaxDelay {
			next = cfg.MaxDelay
		}
		delay = next
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

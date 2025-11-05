package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

type commandOpts struct {
	suppress    []string
	suppressLog string
	quiet       bool
}

func (s *Shaper) execCommand(ctx context.Context, name string, args []string, opts commandOpts) error {
	argStr := strings.Join(args, " ")

	executor := ensureExecutor(s.executor)

	output, err := executor.Run(ctx, name, args)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}

		outStr := strings.TrimSpace(output)
		errStr := err.Error()

		if len(opts.suppress) > 0 && (containsAny(outStr, opts.suppress) || containsAny(errStr, opts.suppress)) {
			if opts.suppressLog != "" && !opts.quiet && s.logger != nil && outStr != "" {
				s.logger.Debug(opts.suppressLog, slog.String("cmd", name), slog.String("args", argStr), slog.String("output", outStr))
			}
			return nil
		}

		return fmt.Errorf("command %s %s: %w", name, argStr, err)
	}

	if !opts.quiet && s.logger != nil && strings.TrimSpace(output) != "" {
		s.logger.Debug("command output", slog.String("cmd", name), slog.String("args", argStr), slog.String("output", output))
	}

	return nil
}

func (s *Shaper) run(ctx context.Context, name string, args ...string) error {
	return s.execCommand(ctx, name, args, commandOpts{})
}

func (s *Shaper) runOptional(ctx context.Context, name string, args []string, suppressed []string) error {
	// Do not spam logs for optional commands; suppress all expected failures quietly
	return s.execCommand(ctx, name, args, commandOpts{
		suppress: suppressed,
		quiet:    true,
	})
}

// runQuiet runs a command without logging warnings
func (s *Shaper) runQuiet(ctx context.Context, name string, args ...string) error {
	return s.execCommand(ctx, name, args, commandOpts{quiet: true})
}

// replaceFilter safely replaces a tc filter by deleting first (ignoring errors) then adding.
func (s *Shaper) replaceFilter(ctx context.Context, cfg FilterConfig) error {
	_ = s.runQuiet(ctx, "tc", cfg.DeleteArgs()...)
	return s.run(ctx, "tc", cfg.AddArgs()...)
}

func containsAny(message string, substrings []string) bool {
	if message == "" || len(substrings) == 0 {
		return false
	}
	lower := strings.ToLower(message)
	for _, sub := range substrings {
		if sub == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// runGetOutput executes a command and returns combined stdout/stderr as string without logging on success
func (s *Shaper) runGetOutput(ctx context.Context, name string, args ...string) (string, error) {
	return ensureExecutor(s.executor).Run(ctx, name, args)
}

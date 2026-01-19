package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"tcsss/internal/infra"
)

// QdiscConfig describes a traffic control qdisc operation.
type QdiscConfig struct {
	Device  string
	Root    bool
	Parent  string
	Handle  string
	Kind    string
	Options []string
}

// ReplaceArgs renders the tc arguments required to replace the qdisc.
func (qc QdiscConfig) ReplaceArgs() []string {
	args := []string{"qdisc", "replace", "dev", qc.Device}

	switch {
	case qc.Root:
		args = append(args, "root")
	case qc.Parent != "":
		args = append(args, "parent", qc.Parent)
	}

	if qc.Handle != "" {
		args = append(args, "handle", qc.Handle)
	}

	if qc.Kind != "" {
		args = append(args, qc.Kind)
	}
	if len(qc.Options) > 0 {
		args = append(args, qc.Options...)
	}
	return args
}

// FilterConfig holds tc filter parameters applied via replaceFilter.
type FilterConfig struct {
	Device   string
	Parent   string
	Protocol string
	Pref     string
	Kind     string
	Actions  []string
}

// DeleteArgs renders the tc arguments to delete an existing filter instance.
func (fc FilterConfig) DeleteArgs() []string {
	return []string{
		"filter", "del",
		"dev", fc.Device,
		"parent", fc.Parent,
		"protocol", fc.Protocol,
		"pref", fc.Pref,
	}
}

// AddArgs renders the tc arguments to add a filter.
func (fc FilterConfig) AddArgs() []string {
	args := []string{
		"filter", "add",
		"dev", fc.Device,
		"parent", fc.Parent,
		"protocol", fc.Protocol,
		"pref", fc.Pref,
		fc.Kind,
	}
	if len(fc.Actions) > 0 {
		args = append(args, fc.Actions...)
	}
	return args
}

func splitQdiscSpec(spec []string) (string, []string) {
	if len(spec) == 0 {
		return "", nil
	}
	kind := spec[0]
	if len(spec) == 1 {
		return kind, nil
	}
	options := make([]string, len(spec)-1)
	copy(options, spec[1:])
	return kind, options
}

func rootQdiscConfig(device string, spec []string) QdiscConfig {
	kind, options := splitQdiscSpec(spec)
	return QdiscConfig{
		Device:  device,
		Root:    true,
		Kind:    kind,
		Options: options,
	}
}

func ifbRootQdiscConfig(ifb string, spec []string) QdiscConfig {
	return rootQdiscConfig(ifb, spec)
}

func ingressQdiscConfig(device string) QdiscConfig {
	return QdiscConfig{
		Device: device,
		Handle: IngressHandle,
		Kind:   "ingress",
	}
}

type commandOpts struct {
	suppress    []string
	suppressLog string
	quiet       bool
}

func (s *Shaper) execCommand(ctx context.Context, name string, args []string, opts commandOpts) error {
	argStr := strings.Join(args, " ")

	executor := infra.EnsureExecutor(s.executor)

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

// runQuiet runs a command without logging warnings.
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

// runGetOutput executes a command and returns combined stdout/stderr as string without logging on success.
func (s *Shaper) runGetOutput(ctx context.Context, name string, args ...string) (string, error) {
	return infra.EnsureExecutor(s.executor).Run(ctx, name, args)
}

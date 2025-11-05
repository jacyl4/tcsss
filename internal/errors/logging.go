package errors

import "log/slog"

// AttrsToArgs converts slog.Attr slice to []any for use with structured logging.
func AttrsToArgs(attrs []slog.Attr) []any {
	if len(attrs) == 0 {
		return nil
	}
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return args
}

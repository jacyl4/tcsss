package traffic

import (
	"log/slog"

	terr "tcsss/internal/errors"
)

func (s *Shaper) handleCategorizedError(message, iface string, err error, defaultCategory terr.Category) {
	if s.logger == nil || err == nil {
		return
	}

	category := defaultCategory
	var ctxMap map[string]any
	if typed, ok := err.(*terr.Error); ok && typed != nil {
		category = typed.Category
		ctxMap = typed.Context.ToMap()
	}

	attrs := []slog.Attr{
		slog.String("category", category.String()),
		slog.String("error", err.Error()),
	}
	if iface != "" {
		attrs = append(attrs, slog.String("interface", iface))
	}
	if len(ctxMap) > 0 {
		attrs = append(attrs, slog.Any("context", ctxMap))
	}

	switch category {
	case terr.CategoryOptional:
		s.logger.Debug(message, terr.AttrsToArgs(attrs)...)
	default:
		s.logger.Error(message, terr.AttrsToArgs(attrs)...)
	}
}

func (s *Shaper) logOptional(message, iface string, err error, ctx terr.ErrorContext) {
	if err == nil {
		return
	}
	s.handleCategorizedError(message, iface, terr.New(terr.CategoryOptional, err, ctx), terr.CategoryOptional)
}

func wrapInterfaceError(err error, iface, operation string, extras terr.ErrorContext) error {
	if err == nil {
		return nil
	}
	ctx := terr.ErrorContext{Operation: operation, Interface: iface}.Merge(extras)
	return terr.New(terr.CategoryRecoverable, err, ctx)
}

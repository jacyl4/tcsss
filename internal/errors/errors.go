package errors

import (
	"fmt"
)

// Category classifies an error to guide handling strategy.
type Category int

const (
	CategoryCritical Category = iota
	CategoryRecoverable
	CategoryOptional
)

func (c Category) String() string {
	switch c {
	case CategoryCritical:
		return "critical"
	case CategoryRecoverable:
		return "recoverable"
	case CategoryOptional:
		return "optional"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// Error wraps an underlying error with a handling category and optional context.
type Error struct {
	Category Category
	Err      error
	Context  ErrorContext
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	ctxMap := e.Context.ToMap()
	if len(ctxMap) == 0 {
		return fmt.Sprintf("[%s] %v", e.Category, e.Err)
	}
	return fmt.Sprintf("[%s] %v (context=%v)", e.Category, e.Err, ctxMap)
}

// Unwrap exposes the wrapped root cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// New constructs an Error with the provided category, cause, and context.
func New(category Category, err error, context ErrorContext) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Category: category,
		Err:      err,
		Context:  context,
	}
}

// WrapRecoverable wraps an existing error as recoverable while merging context maps.
func WrapRecoverable(err error, operation string, contexts ...ErrorContext) *Error {
	if err == nil {
		return nil
	}
	ctx := ErrorContext{Operation: operation}
	for _, c := range contexts {
		ctx = ctx.Merge(c)
	}
	return New(CategoryRecoverable, err, ctx)
}

// InterfaceError annotates an error with interface information.
func InterfaceError(err error, iface, operation string) *Error {
	return WrapRecoverable(err, operation, ErrorContext{Interface: iface})
}

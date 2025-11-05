package errors

import (
	"errors"
	"strings"
)

// MultiError aggregates multiple errors and implements the error interface.
type MultiError struct {
	Errors []error
}

// Add appends an error to the collection if it is non-nil.
func (m *MultiError) Add(err error) {
	if err != nil {
		m.Errors = append(m.Errors, err)
	}
}

// Len returns the number of collected errors.
func (m *MultiError) Len() int {
	return len(m.Errors)
}

// Error implements the error interface by joining all child error messages.
func (m *MultiError) Error() string {
	if len(m.Errors) == 0 {
		return ""
	}
	messages := make([]string, 0, len(m.Errors))
	for _, err := range m.Errors {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	return strings.Join(messages, "; ")
}

// ErrorOrNil returns nil when no errors are recorded, otherwise the MultiError itself.
func (m *MultiError) ErrorOrNil() error {
	if len(m.Errors) == 0 {
		return nil
	}
	return m
}

// Unwrap returns all wrapped errors for use with errors.Is and errors.As.
func (m *MultiError) Unwrap() []error {
	if m == nil {
		return nil
	}
	return m.Errors
}

// Is reports whether any error in the collection matches target.
func (m *MultiError) Is(target error) bool {
	for _, err := range m.Errors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// As finds the first error in the collection that matches target type.
func (m *MultiError) As(target any) bool {
	for _, err := range m.Errors {
		if errors.As(err, target) {
			return true
		}
	}
	return false
}

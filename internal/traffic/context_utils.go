package traffic

import (
	"context"
	"errors"
)

// isContextError reports whether an error is caused by context cancellation or deadline.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

package responsecontract

import (
	"context"
	"io"
	"net/http"
	"time"
)

// RequestContext owns the total budget through body reading and decoding.
// Saturate very large second values instead of overflowing time.Duration.
func RequestContext(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		return context.WithCancel(parent)
	}
	budget := time.Duration(1<<63 - 1)
	if uint64(seconds) <= uint64(budget/time.Second) {
		budget = time.Duration(seconds) * time.Second
	}
	return context.WithTimeout(parent, budget)
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Timer callbacks can lag the deadline, including a concurrent EOF.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// ReadContext must not turn an EOF racing cancellation into a successful body.
// Network interruption remains owned by the request context and transport.
func ReadContext(ctx context.Context, body io.Reader, headers http.Header, maximum int64) ([]byte, string, error) {
	if err := contextError(ctx); err != nil {
		return nil, "", err
	}
	wire, display, err := Read(body, headers, maximum)
	if cause := contextError(ctx); cause != nil {
		return nil, "", cause
	}
	return wire, display, err
}

package fstest

import (
	"context"
	"time"
)

// Retry calls fn up to maxAttempts times with linear back-off.
// It returns nil on the first successful call, or returns the last error if all attempts fail.
func Retry(ctx context.Context, maxAttempts int, delay time.Duration, fn func() error) error {
	if maxAttempts <= 0 {
		return fn()
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// Don't wait after the last attempt
		if i == maxAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return lastErr
}

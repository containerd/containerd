package fstest

import (
	"context"
	"time"
)

// Retry calls fn up to maxAttempts times with linear back-off.
func Retry(ctx context.Context, maxAttempts int, delay time.Duration, fn func() error) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if i == maxAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			continue
		}
	}

	return lastErr
}

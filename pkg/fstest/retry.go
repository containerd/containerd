package fstest

import (
	"context"
	"time"
)

// Retry calls fn up to maxAttempts times with linear back-off.
// It returns nil on the first successful call, or returns the last error if all attempts fail.
func Retry(ctx context.Context, maxAttempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		// Don't wait after the last attempt
		if i == maxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

package fstest

import (
	"context"
	"time"
)

// Retry calls fn up to maxAttempts times with a **linear** back-off.
// It returns nil on the first successful call,或在最後一次仍失敗時回傳該 error。
func Retry(ctx context.Context, maxAttempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		// 最後一次失敗就直接回傳，不再等待
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

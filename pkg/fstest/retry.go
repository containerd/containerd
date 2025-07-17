/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

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

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

package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDo(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var k struct{}
	v := "incontext"
	ctx = context.WithValue(ctx, k, v) //nolint:staticcheck

	assert.Nil(t, contextError(ctx))
	assert.Equal(t, ctx.Value(k), v)

	cancel()
	assert.Error(t, contextError(ctx))
	assert.Equal(t, ctx.Value(k), v)

	t.Run("with canceled context", func(t *testing.T) {
		t.Parallel()
		Do(ctx, func(ctx context.Context) {
			assert.NoError(t, contextError(ctx))
			assert.Equal(t, ctx.Value(k), v)
		})
	})

	t.Run("wrap with cancel", func(t *testing.T) {
		t.Parallel()
		Do(ctx, func(ctx context.Context) {
			ctx, cancelFn := context.WithCancel(ctx)
			cancelFn()
			assert.ErrorIs(t, contextError(ctx), context.Canceled)
			assert.Equal(t, ctx.Value(k), v)
		})
	})

	// canceled after 10 seconds
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{})
		called := make(chan struct{})

		Do(context.Background(), func(ctx context.Context) {
			close(called)
			select {
			case <-ctx.Done():
				close(done)
			case <-time.After(11 * time.Second):
				t.Error("context was not canceled in time")
			}
		})

		select {
		case <-done:
			// success
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for context cancellation")
		}

		select {
		case <-called:
			// success
		default:
			t.Error("do function was not called")
		}
	})
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

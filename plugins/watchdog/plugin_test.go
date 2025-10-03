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

package watchdog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockWatchdogClient struct {
	enabledVal time.Duration
	enabledErr error
	notifyAck  bool
	notifyErr  error

	notifyCounter int
}

func (m *mockWatchdogClient) sdWatchdogEnabled() (time.Duration, error) {
	return m.enabledVal, m.enabledErr
}

func (m *mockWatchdogClient) sdNotify() (bool, error) {
	m.notifyCounter++
	return m.notifyAck, m.notifyErr
}

func TestNewWatchdogNotifier(t *testing.T) {
	for _, tt := range []struct {
		name string
		wc   *mockWatchdogClient
		err  *string
	}{{
		name: "Watchdog enabled",
		wc:   &mockWatchdogClient{enabledVal: 5},
		err:  nil,
	}, {
		name: "Watchdog not enabled",
		wc:   &mockWatchdogClient{enabledVal: 0},
		err:  ptr("systemd watchdog not enabled for this PID"),
	}, {
		name: "Watchdog enabled with error",
		wc: &mockWatchdogClient{
			enabledVal: 6,
			enabledErr: errors.New("mock error"),
		},
		err: ptr("cannot determine if watchdog is enabled:"),
	}} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newWatchdogNotifier(nil, withWatchdogClient(tt.wc))
			if tt.err != nil {
				assert.ErrorContains(t, err, *tt.err)
			}
		})
	}
}

type mockHealthy struct {
	// Number of times it will be healthy
	healthyLimit int
	err          error
	delay        time.Duration

	counter int
}

func (h *mockHealthy) healthy(ctx context.Context) (bool, error) {
	h.counter++
	h.healthyLimit--
	select {
	case <-ctx.Done():
		return false, errors.New("context done")
	case <-time.After(h.delay):
	}
	if h.healthyLimit <= 0 {
		return false, h.err
	}
	return true, nil
}

func TestWatchdogNotify(t *testing.T) {
	for _, tt := range []struct {
		name    string
		wc      *mockWatchdogClient
		healthy *mockHealthy
		check   func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient)
	}{
		{
			name: "ok",
			wc: &mockWatchdogClient{
				enabledVal: 2 * time.Second,
				notifyAck:  true,
			},
			healthy: &mockHealthy{
				healthyLimit: 100,
				delay:        100 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				limit := int(2 * time.Second / 100 / time.Millisecond)
				assert.GreaterOrEqual(t, h.counter, 2)
				assert.LessOrEqual(t, h.counter, limit)
				assert.GreaterOrEqual(t, wc.notifyCounter, 2)
				assert.LessOrEqual(t, wc.notifyCounter, limit)
			},
		}, {
			name: "not healthy",
			wc: &mockWatchdogClient{
				enabledVal: 300 * time.Millisecond,
				notifyAck:  true,
			},
			healthy: &mockHealthy{
				healthyLimit: 2,
				delay:        100 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				assert.GreaterOrEqual(t, h.counter, 1)
				assert.LessOrEqual(t, h.counter, 2)
				assert.GreaterOrEqual(t, wc.notifyCounter, 1)
				assert.LessOrEqual(t, wc.notifyCounter, 2)
			},
		}, {
			name: "not healthy with error",
			wc: &mockWatchdogClient{
				enabledVal: 300 * time.Millisecond,
				notifyAck:  true,
			},
			healthy: &mockHealthy{
				healthyLimit: 2,
				err:          errors.New("healthy error"),
				delay:        100 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				assert.Equal(t, h.counter, 2)
				assert.Equal(t, wc.notifyCounter, 1)
			},
		}, {
			name: "not ack'ed retries",
			wc: &mockWatchdogClient{
				enabledVal: 1000 * time.Millisecond,
				notifyAck:  false,
			},
			healthy: &mockHealthy{
				healthyLimit: 100,
				delay:        50 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				assert.GreaterOrEqual(t, h.counter, 2)
				assert.LessOrEqual(t, h.counter, 5)
				assert.GreaterOrEqual(t, wc.notifyCounter, 2)
				assert.LessOrEqual(t, wc.notifyCounter, 5)
			},
		}, {
			name: "ack error",
			wc: &mockWatchdogClient{
				enabledVal: 100 * time.Millisecond,
				notifyAck:  false,
				notifyErr:  errors.New("sdnotify error"),
			},
			healthy: &mockHealthy{
				healthyLimit: 100,
				delay:        5 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				assert.Equal(t, h.counter, 1)
				assert.Equal(t, wc.notifyCounter, 1)
			},
		}, {
			name: "slow health",
			wc: &mockWatchdogClient{
				enabledVal: 100 * time.Millisecond,
				notifyAck:  true,
			},
			healthy: &mockHealthy{
				healthyLimit: 100,
				delay:        300 * time.Millisecond,
			},
			check: func(t *testing.T, h *mockHealthy, wc *mockWatchdogClient) {
				assert.Equal(t, h.counter, 1)
				assert.Equal(t, wc.notifyCounter, 0)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wn, err := newWatchdogNotifier(tt.healthy.healthy, withWatchdogClient(tt.wc))
			assert.NoError(t, err)

			ctx := context.Background()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			go wn.Notify(ctx)

			<-time.After(tt.wc.enabledVal * 2)
			cancel()
			wn.Wait()
			tt.check(t, tt.healthy, tt.wc)
		})
	}
}

func ptr[T any](t T) *T { return &t }

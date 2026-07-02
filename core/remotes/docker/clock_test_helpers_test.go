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

package docker

import (
	"sync"
	"sync/atomic"
	"time"
)

type mockClock struct {
	now        int64
	mu         sync.Mutex
	lastTicker *mockTicker
}

func (mc *mockClock) Now() time.Time {
	return time.Unix(0, atomic.LoadInt64(&mc.now))
}

func (mc *mockClock) Since(t time.Time) time.Duration {
	return time.Unix(0, atomic.LoadInt64(&mc.now)).Sub(t)
}

func (mc *mockClock) Advance(d time.Duration) {
	atomic.AddInt64(&mc.now, int64(d))
}

func (mc *mockClock) NewTicker(d time.Duration) Ticker {
	t := &mockTicker{ch: make(chan time.Time, 1)}
	mc.mu.Lock()
	mc.lastTicker = t
	mc.mu.Unlock()
	return t
}

type mockTicker struct {
	ch chan time.Time
}

func (t *mockTicker) C() <-chan time.Time { return t.ch }
func (t *mockTicker) Stop()               {}
func (t *mockTicker) Tick()               { t.ch <- time.Time{} }

func NewActivityTrackerWithClock(clock Clock) *ActivityTracker {
	return &ActivityTracker{
		clock: clock,
	}
}

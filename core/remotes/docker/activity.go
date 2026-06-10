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
	"sync/atomic"
	"time"
)

type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

type mockClock struct {
	now int64
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

type ActivityTrackerInterface interface {
	Touch()
	Stalled(window time.Duration) bool
	TimeSinceLastActivity() time.Duration
}

type ActivityTracker struct {
	lastActivity atomic.Int64
	window       time.Duration
	clock        Clock
}

func NewActivityTracker(window time.Duration) *ActivityTracker {
	return &ActivityTracker{
		window: window,
		clock:  realClock{},
	}
}

func NewActivityTrackerWithClock(clock Clock) *ActivityTracker {
	return &ActivityTracker{
		clock: clock,
	}
}

func (t *ActivityTracker) Touch() {
	now := t.clock.Now().UnixNano()
	t.lastActivity.Store(now)
}

func (t *ActivityTracker) Stalled(window time.Duration) bool {
	if window <= 0 {
		window = t.window
	}
	if window <= 0 {
		return false
	}
	last := t.lastActivity.Load()
	if last == 0 {
		return false
	}
	lastTime := time.Unix(0, last)
	return t.clock.Since(lastTime) > window
}

func (t *ActivityTracker) TimeSinceLastActivity() time.Duration {
	last := t.lastActivity.Load()
	if last <= 0 {
		return 0
	}
	lastTime := time.Unix(0, last)
	return t.clock.Since(lastTime)
}

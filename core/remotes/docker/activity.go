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
	NewTicker(d time.Duration) Ticker
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}
type realTicker struct{ *time.Ticker }

func (realClock) Now() time.Time                   { return time.Now() }
func (realClock) Since(t time.Time) time.Duration  { return time.Since(t) }
func (realClock) NewTicker(d time.Duration) Ticker { return &realTicker{time.NewTicker(d)} }
func (t *realTicker) C() <-chan time.Time          { return t.Ticker.C }
func (t *realTicker) Stop()                        { t.Ticker.Stop() }

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

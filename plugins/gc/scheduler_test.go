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

package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/internal/tomlext"
	"github.com/containerd/containerd/v2/pkg/gc"
	"github.com/stretchr/testify/assert"
)

func TestPauseThreshold(t *testing.T) {
	cfg := &config{
		// With 100μs, gc should run about every 5ms
		PauseThreshold: 0.02,
	}
	tc := &testCollector{
		d: time.Microsecond * 100,
	}

	scheduler := newScheduler(tc, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scheduler.run(ctx)

	// Ensure every possible GC cycle runs
	go func() {
		tick := time.NewTicker(time.Microsecond * 100)
		for {
			select {
			case <-tick.C:
				tc.trigger(true)
			case <-ctx.Done():
				return
			}
		}
	}()

	time.Sleep(time.Millisecond * 15)

	if c := tc.runCount(); c > 4 {
		t.Fatalf("unexpected gc run count %d, expected less than 5", c)
	}
}

func TestDeletionThreshold(t *testing.T) {
	cfg := &config{
		// Prevent GC from scheduling again before check
		PauseThreshold:    0.001,
		DeletionThreshold: 5,
	}
	tc := &testCollector{
		d: time.Second,
	}

	scheduler := newScheduler(tc, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scheduler.run(ctx)

	// Block until next GC finishes
	gcWait := make(chan struct{})
	go func() {
		scheduler.wait(ctx, false)
		close(gcWait)
	}()

	// Increment deletion count 5, checking GC hasn't run in
	// between each call
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond)
		if c := tc.runCount(); c != 0 {
			t.Fatalf("GC ran unexpectedly")
		}
		tc.trigger(true)
	}

	select {
	case <-gcWait:
	case <-time.After(time.Millisecond * 30):
		t.Fatal("GC wait timed out")
	}

	if c := tc.runCount(); c != 1 {
		t.Fatalf("unexpected gc run count %d, expected 1", c)
	}
}

func TestTrigger(t *testing.T) {
	var (
		cfg = &config{}
		tc  = &testCollector{
			d: time.Millisecond * 10,
		}
		ctx, cancel = context.WithCancel(context.Background())
		scheduler   = newScheduler(tc, cfg)
		stats       gc.Stats
		err         error
	)

	defer cancel()
	go scheduler.run(ctx)

	// Block until next GC finishes
	gcWait := make(chan struct{})
	go func() {
		stats, err = scheduler.ScheduleAndWait(ctx)
		close(gcWait)
	}()

	select {
	case <-gcWait:
	case <-time.After(time.Millisecond * 10):
		t.Fatal("GC wait timed out")
	}

	if err != nil {
		t.Fatalf("GC failed: %#v", err)
	}

	assert.Equal(t, tc.d, stats.Elapsed())

	if c := tc.runCount(); c != 1 {
		t.Fatalf("unexpected gc run count %d, expected 1", c)
	}
}

func TestStartupDelay(t *testing.T) {
	var (
		startupDelay = time.Millisecond * 20
		cfg          = &config{
			// Prevent GC from scheduling again before check
			PauseThreshold: 0.001,
			StartupDelay:   tomlext.Duration(startupDelay),
		}
		tc = &testCollector{
			d: time.Second,
		}
		ctx, cancel = context.WithCancel(context.Background())
		scheduler   = newScheduler(tc, cfg)
	)
	defer cancel()

	t1 := time.Now()
	go scheduler.run(ctx)
	_, err := scheduler.wait(ctx, false)
	if err != nil {
		t.Fatalf("gc failed with error: %s", err)
	}
	d := time.Since(t1)

	if c := tc.runCount(); c != 1 {
		t.Fatalf("unexpected gc run count %d, expected 1", c)
	}

	if d < startupDelay {
		t.Fatalf("expected startup delay to be longer than %s, actual %s", startupDelay, d)
	}

}

type testCollector struct {
	d  time.Duration
	gc int
	m  sync.Mutex

	callbacks []func(bool)
}

func (tc *testCollector) trigger(delete bool) {
	for _, f := range tc.callbacks {
		f(delete)
	}
}

func (tc *testCollector) runCount() int {
	tc.m.Lock()
	c := tc.gc
	tc.m.Unlock()
	return c
}

func (tc *testCollector) RegisterMutationCallback(f func(bool)) {
	tc.callbacks = append(tc.callbacks, f)
}

func (tc *testCollector) GarbageCollect(context.Context) (gc.Stats, error) {
	tc.m.Lock()
	tc.gc++
	tc.m.Unlock()
	return gcStats{elapsed: tc.d}, nil
}

type gcStats struct {
	elapsed time.Duration
}

// Elapsed returns the duration which elapsed during a collection
func (s gcStats) Elapsed() time.Duration {
	return s.elapsed
}

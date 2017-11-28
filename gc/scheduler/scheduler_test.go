package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/metadata"
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
	if c := tc.runCount(); c < 3 || c > 4 {
		t.Fatalf("unexpected gc run count %d, expected between 5 and 6", c)
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
	case <-time.After(time.Millisecond * 10):
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
		stats       metadata.GCStats
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

	if stats.MetaD != tc.d {
		t.Fatalf("unexpected gc duration: %s, expected %d", stats.MetaD, tc.d)
	}

	if c := tc.runCount(); c != 1 {
		t.Fatalf("unexpected gc run count %d, expected 1", c)
	}
}

func TestStartupDelay(t *testing.T) {
	var (
		cfg = &config{
			// Prevent GC from scheduling again before check
			PauseThreshold: 0.001,
			StartupDelay:   duration(time.Millisecond),
		}
		tc = &testCollector{
			d: time.Second,
		}
		ctx, cancel = context.WithCancel(context.Background())
		scheduler   = newScheduler(tc, cfg)
	)
	defer cancel()
	go scheduler.run(ctx)

	time.Sleep(time.Millisecond * 5)

	if c := tc.runCount(); c != 1 {
		t.Fatalf("unexpected gc run count %d, expected 1", c)
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

func (tc *testCollector) GarbageCollect(context.Context) (metadata.GCStats, error) {
	tc.m.Lock()
	tc.gc++
	tc.m.Unlock()
	return metadata.GCStats{
		MetaD: tc.d,
	}, nil
}

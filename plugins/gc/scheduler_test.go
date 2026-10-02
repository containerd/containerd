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
	ctx := t.Context()

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
	ctx := t.Context()

	go scheduler.run(ctx)

	// Block until next GC finishes
	gcWait := make(chan struct{})
	go func() {
		scheduler.wait(ctx, false)
		close(gcWait)
	}()

	// Increment deletion count 5, checking GC hasn't run in
	// between each call
	for range 5 {
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
		cfg       = &config{}
		tc        = &testCollector{d: time.Millisecond * 10}
		ctx       = t.Context()
		scheduler = newScheduler(tc, cfg)
		stats     gc.Stats
		err       error
	)

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
		tc        = &testCollector{d: time.Second}
		ctx       = t.Context()
		scheduler = newScheduler(tc, cfg)
	)

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

// TestCloseWaitsForRunLoop verifies that Close() cancels the run loop and
// blocks until an in-progress GC completes and the loop exits.
func TestCloseWaitsForRunLoop(t *testing.T) {
	bc := &blockingCollector{
		testCollector: &testCollector{},
		started:       make(chan struct{}),
		release:       make(chan struct{}),
		completed:     make(chan struct{}),
	}

	scheduler := newScheduler(bc, &config{})

	// Wire the cancel func into the scheduler so that Close() is responsible
	// for stopping the run loop, mirroring how the plugin InitFn starts it.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scheduler.cancel = cancel
	go scheduler.run(ctx)

	// Send a trigger event to schedule an immediate GC.
	go func() {
		scheduler.eventC <- mutationEvent{ts: time.Now()}
	}()

	// Wait for GC to start.
	select {
	case <-bc.started:
	case <-t.Context().Done():
		t.Fatal("timed out waiting for GC to start")
	}

	// Call Close() while GC is in-flight. Close() is responsible for
	// cancelling the run loop; the test does not cancel it directly.
	closeDone := make(chan struct{})
	go func() {
		scheduler.Close()
		close(closeDone)
	}()

	// Unblock GC; run() will exit on the next select iteration once Close()
	// has cancelled the context.
	close(bc.release)

	select {
	case <-closeDone:
	case <-t.Context().Done():
		t.Fatal("Close() did not return after GC finished")
	}

	// Verify GC completed before Close() returned.
	select {
	case <-bc.completed:
	default:
		t.Fatal("Close() returned before GC finished")
	}
}

// TestSendersDoNotLeakAfterClose verifies that the event senders
// (mutationCallback and the wait trigger) do not block forever on the
// unbuffered eventC once the run loop has exited. Before the doneC select was
// added, each post-shutdown mutation leaked a goroutine blocked on the send.
func TestSendersDoNotLeakAfterClose(t *testing.T) {
	scheduler := newScheduler(&testCollector{}, &config{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scheduler.cancel = cancel
	go scheduler.run(ctx)

	assert.NoError(t, scheduler.Close())

	// ScheduleAndWait after close must return promptly rather than register a
	// waiter that nothing will ever complete.
	_, err := scheduler.ScheduleAndWait(context.Background())
	assert.ErrorIs(t, err, errSchedulerClosed)

	// Both mutationCallback and the wait trigger deliver events via
	// go s.sendEvent(e). Drive sendEvent directly under a WaitGroup so the
	// assertion tracks the exact senders we spawn. Every send targets the
	// now-unread eventC; with the doneC select each sender returns, without
	// it each would block forever on the channel send.
	const senders = 100
	var wg sync.WaitGroup
	for range senders {
		wg.Go(func() {
			scheduler.sendEvent(mutationEvent{ts: time.Now(), mutation: true, dirty: true})
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("sender goroutines leaked after Close: blocked on eventC send")
	}
}

// TestWaitUnblockedByClose verifies that a pending wait() caller is released
// when the run loop exits, rather than blocking until its own context is done.
func TestWaitUnblockedByClose(t *testing.T) {
	scheduler := newScheduler(&testCollector{}, &config{})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scheduler.cancel = cancel
	go scheduler.run(ctx)

	// Register a waiter that no collection will ever satisfy, using a context
	// independent of the run loop's so that only Close() can release it.
	errC := make(chan error, 1)
	go func() {
		_, err := scheduler.wait(t.Context(), false)
		errC <- err
	}()

	assert.NoError(t, scheduler.Close())

	select {
	case err := <-errC:
		assert.ErrorIs(t, err, errSchedulerClosed)
	case <-t.Context().Done():
		t.Fatal("wait did not unblock after Close")
	}
}

// blockingCollector is a test collector that blocks until release is closed, allowing
// tests to verify that Close() waits for an in-progress GC to complete before returning.
type blockingCollector struct {
	*testCollector
	started   chan struct{}
	release   chan struct{}
	completed chan struct{}
}

func (bc *blockingCollector) GarbageCollect(ctx context.Context) (gc.Stats, error) {
	close(bc.started)
	defer close(bc.completed)

	// Block until test releases.
	<-bc.release
	return gcStats{}, nil
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

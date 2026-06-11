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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityPipeWriterWriteCallsTouch(t *testing.T) {
	tracker := &mockActivityTracker{}

	pr, pw := io.Pipe()
	apw := &activityPipeWriter{pw: pw, tracker: tracker}

	done := make(chan struct{})
	go func() {
		defer close(done)
		data := make([]byte, 1024)
		pr.Read(data)
	}()

	data := []byte("test data")
	n, err := apw.Write(data)

	pw.Close()
	<-done

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, int32(1), tracker.touchCount, "Touch() should be called once on write")
}

func TestPushWriterWriteWithoutActivity(t *testing.T) {
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, nil)

	pr, pipeWriter := io.Pipe()
	pw.pipeC <- &activityPipeWriter{pw: pipeWriter, tracker: nil}

	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(io.Discard, pr)
	}()

	data := []byte("test data")
	n, err := pw.Write(data)

	pipeWriter.Close()
	<-done

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
}

func TestNoopActivityTrackerNoPanic(t *testing.T) {
	noop := &noopActivityTracker{}

	assert.NotPanics(t, func() {
		noop.Touch()
	})
	assert.NotPanics(t, func() {
		noop.Stalled(5 * time.Second)
	})
	assert.NotPanics(t, func() {
		noop.TimeSinceLastActivity()
	})
}

func TestCommitWithActivityNoTimeout(t *testing.T) {
	clock := &mockClock{now: time.Now().UnixNano()}
	tracker := NewActivityTrackerWithClock(clock)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusCreated)

	pw.setResponse(resp.Result())

	go func() {
		clock.Advance(100 * time.Millisecond)
		tracker.Touch()
	}()

	ctx := context.Background()
	err := pw.Commit(ctx, 0, "")

	assert.NoError(t, err)
}

func TestCommitStalledTimeout(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)
	window := 50 * time.Millisecond

	tracker.Touch()

	if tracker.Stalled(window) {
		t.Error("Stalled() should return false when within window after Touch()")
	}

	clock.Advance(60 * time.Millisecond)

	if !tracker.Stalled(window) {
		t.Error("Stalled() should return true when past window after Touch()")
	}

	clock.Advance(10 * time.Millisecond)
	tracker.Touch()

	if tracker.Stalled(window) {
		t.Error("Stalled() should return false when within window after re-Touch()")
	}
}

func TestCommitCompletesWhenResponseReceived(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusCreated)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	done := make(chan error, 1)

	go func() {
		done <- pw.Commit(ctx, 0, "")
	}()

	// Integration test with httptest.Server - server responds immediately,
	// so timeout is just a safety net for CI environments.
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Commit should have completed")
	}
}

func TestCommitWithNilActivity(t *testing.T) {
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, nil)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusCreated)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	done := make(chan error, 1)

	go func() {
		done <- pw.Commit(ctx, 0, "")
	}()

	// Integration test with httptest.Server - server responds immediately,
	// so timeout is just a safety net for CI environments.
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Commit should have completed")
	}
}

func TestConcurrentWriteWithActivityTracking(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  1024,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)

	pr, pipeWriter := io.Pipe()
	// Pre-set the pipe so all writers see pw.pipe != nil immediately.
	// Sending only via pipeC would cause a race: multiple writers could
	// see pw.pipe == nil and all block on pipeC, but only one item exists.
	pw.pipeMu.Lock()
	pw.pipe = &activityPipeWriter{pw: pipeWriter, tracker: tracker}
	pw.pipeMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(io.Discard, pr)
	}()

	var wg sync.WaitGroup
	numWriters := 5
	numWrites := 100

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numWrites; j++ {
				data := []byte{byte(id), byte(j)}
				_, err := pw.Write(data)
				if err != nil && err != io.ErrClosedPipe {
					assert.NoError(t, err)
				}
			}
		}(i)
	}

	wg.Wait()
	pipeWriter.Close()
	pr.Close()
	<-done
}

func TestActivityPipeWriterClose(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)

	pr, pw := io.Pipe()
	apw := &activityPipeWriter{pw: pw, tracker: tracker}

	apw.Close()

	_ = pr.Close()
}

func TestActivityPipeWriterCloseWithError(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)

	pr, pw := io.Pipe()
	apw := &activityPipeWriter{pw: pw, tracker: tracker}

	apw.CloseWithError(io.EOF)

	_ = pr.Close()
	_ = pw.Close()
}

func TestPushWriterCommitWithActivityCheck(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusOK)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	err := pw.Commit(ctx, 0, "")

	assert.NoError(t, err)
	assert.False(t, tracker.Stalled(5*time.Second))
}

func TestPushWriterCommitWithResponseBeforeStalled(t *testing.T) {
	tracker := NewActivityTracker(1 * time.Second)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:    "test-ref",
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusOK)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	err := pw.Commit(ctx, 0, "")

	assert.NoError(t, err)
}

func TestCommitStallTimeoutWithMockClock(t *testing.T) {
	clk := &mockClock{now: time.Now().UnixNano()}
	tracker := NewActivityTrackerWithClock(clk)
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:   "test-ref",
			Total: 100,
		},
	})

	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, tracker)
	pw.pipe = nil

	tracker.Touch()

	commitDone := make(chan error, 1)
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		close(started)
		commitDone <- pw.Commit(ctx, 0, "")
	}()

	<-started
	// Give Commit time to enter its select loop.
	time.Sleep(50 * time.Millisecond)

	// Advance mock clock past 30s stall window.
	clk.Advance(31 * time.Second)

	// Verify the tracker sees the stall.
	require.True(t, tracker.Stalled(30*time.Second), "tracker should be stalled after 31s advance")

	// Repeatedly tick until Commit responds or ctx expires.
	clk.mu.Lock()
	tick := clk.lastTicker
	clk.mu.Unlock()
	require.NotNil(t, tick)

	commitErr := func() error {
		for i := 0; i < 5000; i++ {
			select {
			case err := <-commitDone:
				return err
			default:
			}
			select {
			case <-tick.C():
			default:
			}
			tick.Tick()
			time.Sleep(time.Millisecond)
		}
		return ctx.Err()
	}()

	require.Error(t, commitErr)
	assert.Contains(t, commitErr.Error(), "no activity for 30s")
}

func TestCommitRespectsContextCancellation(t *testing.T) {
	statusTracker := NewInMemoryTracker()

	statusTracker.SetStatus("test-ref", Status{
		Status: content.Status{
			Ref:   "test-ref",
			Total: 100,
		},
	})

	// nil activity — no stall detection, only ctx cancellation.
	pw := newPushWriter(nil, "test-ref", "", statusTracker, false, nil)
	pw.pipe = nil

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := pw.Commit(ctx, 0, "")

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNewPushWriterWithActivity(t *testing.T) {
	tracker := NewActivityTracker(5 * time.Second)
	statusTracker := NewInMemoryTracker()

	pw := newPushWriter(nil, "test-ref", "sha256:abc123", statusTracker, true, tracker)

	require.NotNil(t, pw)
	assert.Equal(t, "test-ref", pw.ref)
	assert.Equal(t, "sha256:abc123", pw.expected.String())
	assert.True(t, pw.isManifest)
	assert.Equal(t, tracker, pw.activity)
}

func TestNewPushWriterWithoutActivity(t *testing.T) {
	statusTracker := NewInMemoryTracker()
	pw := newPushWriter(nil, "test-ref", "sha256:abc123", statusTracker, false, nil)

	require.NotNil(t, pw)
	assert.Nil(t, pw.activity)
}

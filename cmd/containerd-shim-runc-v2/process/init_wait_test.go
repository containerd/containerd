//go:build !windows

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

package process

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestInitWaitLivenessCheck verifies that Init.Wait() detects a dead process
// via kill(pid, 0) and forces exit even if the exit event was never received.
// This is the fallback mechanism for containerd/containerd#12678.
func TestInitWaitLivenessCheck(t *testing.T) {
	// Use a very large PID that is overwhelmingly unlikely to exist.
	// Skip the test if it unexpectedly does.
	const deadPID = 4194305

	// Verify the PID does not exist before proceeding.
	if err := unix.Kill(deadPID, 0); err != unix.ESRCH {
		t.Skipf("pid %d unexpectedly exists or kill returned %v", deadPID, err)
	}

	// Create a minimal Init with the dead PID and an open waitBlock.
	p := &Init{
		pid:               deadPID,
		waitBlock:         make(chan struct{}),
		initState:         &stoppedState{}, // SetExited on stoppedState is a no-op
		liveCheckInterval: 500 * time.Millisecond,
	}

	// Wait should detect the dead process and return within the check interval.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Wait(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait() returned error: %v", err)
		}
		// Success: Wait detected dead process and returned.
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() did not return within expected time; liveness check failed")
	}
}

// TestInitWaitNormalExit verifies that Init.Wait() returns immediately when
// waitBlock is closed (normal exit path), without waiting for the ticker.
func TestInitWaitNormalExit(t *testing.T) {
	p := &Init{
		pid:       1, // PID 1 always exists, but shouldn't matter
		waitBlock: make(chan struct{}),
		initState: &stoppedState{},
	}

	// Simulate normal exit: close waitBlock.
	close(p.waitBlock)

	ctx := context.Background()
	start := time.Now()
	if err := p.Wait(ctx); err != nil {
		t.Fatalf("Wait() returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("Wait() took too long (%v); should return immediately on closed waitBlock", elapsed)
	}
}

// TestInitWaitContextCancel verifies that Init.Wait() respects context cancellation.
func TestInitWaitContextCancel(t *testing.T) {
	p := &Init{
		pid:       1,
		waitBlock: make(chan struct{}), // never closed
		initState: &stoppedState{},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Wait(ctx)
	}()

	// Cancel context after a short delay.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() did not return after context cancellation")
	}
}

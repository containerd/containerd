//go:build windows

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

package windows

// Characterization test for finding P1-3: prepareMergedCIMLocked must not call
// Unlock when mergeLock.Lock fails. Pre-fix the merge path ignored Lock's error
// and unconditionally deferred Unlock; when Lock failed (request ctx cancelled
// while waiting for a long-running merge) the lock was never held, so kmutex
// panicked with "unlock of unlocked key", crashing the un-recovered daemon.
//
// This test drives the real method against a real kmutex. A cancelled ctx makes
// Lock return immediately (Acquire honours ctx-done with priority), so
// prepareMergedCIM is never reached and no further snapshotter state is needed.
//
// Pre-fix (revert the `if err != nil { return err }` guard inside
// prepareMergedCIMLocked) this test FAILs with a panic; post-fix it PASSes.
//
// Note: this package is Windows-only and cannot be executed on a non-Windows
// host. The platform-independent reproduction of the same mechanism lives in
// internal/kmutex/kmutex_misuse_safety_test.go and runs everywhere.

import (
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/internal/kmutex"
)

func TestBlockCIMSnapshotterSafety_MergeLockErrorNoPanic(t *testing.T) {
	s := &blockCIMSnapshotter{mergeLock: kmutex.New()}

	// Already-cancelled ctx: Lock's Acquire returns ctx.Err() with priority,
	// so the lock is never held — the pre-fix unconditional defer Unlock would
	// panic here.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("prepareMergedCIMLocked panicked on Lock failure (P1-3 regression): %v", r)
			}
		}()
		err = s.prepareMergedCIMLocked(ctx, "parent-key", []string{"a", "b"})
	}()

	if err == nil {
		t.Fatal("expected an error when Lock fails on a cancelled ctx, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled to propagate, got %v", err)
	}
}

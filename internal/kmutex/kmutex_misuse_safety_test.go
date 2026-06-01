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

package kmutex

// Characterization tests for the Lock-error contract that the Windows
// block-CIM snapshotter (plugins/snapshots/windows/block_cimfs.go) relies on.
//
// These run on every platform (kmutex is platform independent) and therefore
// act as the cross-platform reproduction of finding P1-3: the block-CIM merge
// path used to call mergeLock.Lock(ctx, parent) while *ignoring* the returned
// error and then unconditionally `defer mergeLock.Unlock(parent)`. When Lock
// fails (e.g. the request ctx is cancelled while waiting for a long-running
// merge) it does NOT hold the lock — Acquire failed — and kmutex deletes the
// key, so the unconditional Unlock panics with "unlock of unlocked key",
// crashing the (un-recovered) daemon.
//
// The actual call site is Windows-only and cannot run on this host; the
// matching guard for the real code lives in
// plugins/snapshots/windows/block_cimfs_safety_test.go. The two helpers below
// transcribe the pre-fix and post-fix patterns verbatim so the mechanism is
// proven against the real kmutex primitive.
//
// Both tests must pass before and after the fix: they lock kmutex's contract,
// not the snapshotter's behaviour.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// buggyMergeLockPattern mirrors block_cimfs.go:348-349 BEFORE the fix:
//
//	s.mergeLock.Lock(ctx, parent)    // error ignored
//	defer s.mergeLock.Unlock(parent) // unconditional
func buggyMergeLockPattern(ctx context.Context, l KeyedLocker, key string) {
	l.Lock(ctx, key)    //nolint:errcheck // deliberately reproducing the bug
	defer l.Unlock(key) // unconditional Unlock even when Lock failed
}

// fixedMergeLockPattern mirrors block_cimfs.go AFTER the fix:
//
//	if err := s.mergeLock.Lock(ctx, parent); err != nil {
//		return err
//	}
//	defer s.mergeLock.Unlock(parent)
func fixedMergeLockPattern(ctx context.Context, l KeyedLocker, key string) error {
	if err := l.Lock(ctx, key); err != nil {
		return err
	}
	defer l.Unlock(key)
	return nil
}

// TestKeyMutexSafety_LockErrIgnoredThenUnlockPanics proves the P1-3 mechanism:
// when Lock fails on a never-held key, ignoring the error and unconditionally
// Unlocking panics. This documents WHY the caller must check Lock's error.
func TestKeyMutexSafety_LockErrIgnoredThenUnlockPanics(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()

	// Already-cancelled ctx: Acquire returns ctx.Err() with priority even
	// though the semaphore is free, so the key is never actually held. This
	// is the single-request production trigger (CreateContainer reaching the
	// merge with an expired/cancelled request ctx).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from unconditional Unlock after a failed Lock, got none")
		}
		t.Logf("reproduced P1-3 panic: %v", r)
		// After the panic the key must have been deleted (ref hit 0), i.e.
		// the lock was never held.
		assert.Equal(t, 0, len(km.locks))
	}()

	buggyMergeLockPattern(ctx, km, t.Name())
}

// TestKeyMutexSafety_LockErrCheckedNoPanic proves the fix: checking Lock's
// error and returning before registering the Unlock leaves the locker clean
// and never panics.
func TestKeyMutexSafety_LockErrCheckedNoPanic(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var err error
	assert.NotPanics(t, func() {
		err = fixedMergeLockPattern(ctx, km, t.Name())
	})

	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
	// Failed Lock must not leak a klock entry.
	assert.Equal(t, 0, len(km.locks))
}

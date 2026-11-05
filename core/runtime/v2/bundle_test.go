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

package v2

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	// When testutil is imported for one platform (bundle_linux_test.go) it
	// should be imported for all platforms.
	_ "github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bundleDirs returns a context and the root and state dirs to create bundles in.
func bundleDirs(t *testing.T) (context.Context, string, string) {
	dir := t.TempDir()
	ctx := namespaces.WithNamespace(context.Background(), namespaces.Default)
	return ctx, filepath.Join(dir, "root"), filepath.Join(dir, "state")
}

// failDelete makes b.Delete fail, as it does on Windows when a shim that is
// still exiting holds files in the bundle, and returns a function that undoes
// that. A rootfs with content cannot be removed, but is not a live mount.
func failDelete(t *testing.T, b *Bundle) (release func()) {
	busy := filepath.Join(b.Path, "rootfs", "busy")
	require.NoError(t, os.WriteFile(busy, []byte("x"), 0600))
	require.ErrorContains(t, b.Delete(), "failed to remove bundle rootfs")
	return func() { require.NoError(t, os.Remove(busy)) }
}

// TestNewBundleRejectsExistingBundle verifies that a second create for the ID of
// a live bundle fails and leaves that bundle alone.
func TestNewBundleRejectsExistingBundle(t *testing.T) {
	ctx, root, state := bundleDirs(t)
	const id = "existing"

	b, err := NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err)
	live := filepath.Join(b.Path, "live")
	require.NoError(t, os.WriteFile(live, []byte("x"), 0600))

	_, err = NewBundle(ctx, root, state, id, nil)
	require.ErrorIs(t, err, os.ErrExist)
	assert.FileExists(t, live)
	_, err = os.Stat(filepath.Join(b.Path, "work"))
	assert.NoError(t, err, "work link of the live bundle must be kept")

	require.NoError(t, b.Delete())
	_, err = NewBundle(ctx, root, state, id, nil)
	assert.NoError(t, err, "ID must be reusable once the bundle is deleted")
}

// TestNewBundleReplacesLeftoverBundle verifies that NewBundle recovers when the
// Delete of a previous bundle left its directory behind. On Windows,
// atomicDelete's os.Rename can fail while a still-exiting shim holds files in
// the bundle, and the next NewTask must not fail with "file already exists".
func TestNewBundleReplacesLeftoverBundle(t *testing.T) {
	ctx, root, state := bundleDirs(t)
	const id = "leftover"

	b, err := NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err)
	release := failDelete(t, b)
	release()
	stale := filepath.Join(b.Path, "stale")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0600))

	_, err = NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err, "NewBundle should replace the leftover bundle")
	assert.NoFileExists(t, stale)

	_, err = NewBundle(ctx, root, state, id, nil)
	assert.ErrorIs(t, err, os.ErrExist, "the new bundle is live and must not be replaced")
}

// TestNewBundleKeepsBusyLeftover verifies that a leftover bundle is not removed
// through a rootfs that is still in use, and can be replaced once it is not.
func TestNewBundleKeepsBusyLeftover(t *testing.T) {
	ctx, root, state := bundleDirs(t)
	const id = "busy-leftover"

	b, err := NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err)
	release := failDelete(t, b)

	_, err = NewBundle(ctx, root, state, id, nil)
	require.ErrorContains(t, err, "failed to remove bundle rootfs")
	assert.FileExists(t, filepath.Join(b.Path, "rootfs", "busy"))

	release()
	_, err = NewBundle(ctx, root, state, id, nil)
	assert.NoError(t, err)
}

// TestNewBundleConcurrentLeftover verifies that of several creates racing for a
// leftover bundle, exactly one replaces it.
func TestNewBundleConcurrentLeftover(t *testing.T) {
	ctx, root, state := bundleDirs(t)
	const id = "concurrent-leftover"

	b, err := NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err)
	failDelete(t, b)()

	var (
		wg      sync.WaitGroup
		created atomic.Int32
	)
	for range 8 {
		wg.Go(func() {
			if _, err := NewBundle(ctx, root, state, id, nil); err == nil {
				created.Add(1)
			} else {
				assert.ErrorIs(t, err, os.ErrExist)
			}
		})
	}
	wg.Wait()
	assert.EqualValues(t, 1, created.Load())
	_, err = os.Stat(filepath.Join(b.Path, "work"))
	assert.NoError(t, err, "work link of the new bundle must be kept")
}

// TestBundleLocksSamePath verifies that create and delete of one bundle path
// wait for each other, so a Delete cannot publish a stale leftover marker after
// a create. Other paths must not wait.
func TestBundleLocksSamePath(t *testing.T) {
	ctx, root, state := bundleDirs(t)

	b, err := NewBundle(ctx, root, state, "locked", nil)
	require.NoError(t, err)
	require.NoError(t, bundleLocks.Lock(ctx, b.Path))

	_, err = NewBundle(ctx, root, state, "other", nil)
	assert.NoError(t, err, "other paths must not wait")

	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = NewBundle(short, root, state, "locked", nil)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "create must wait for the lock")

	done := make(chan error, 1)
	go func() { done <- b.Delete() }()
	select {
	case err := <-done:
		t.Fatalf("Delete must wait for the lock, got %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	bundleLocks.Unlock(b.Path)
	require.NoError(t, <-done)
}

// TestBundleDeleteWithStaleAtomicPath verifies that a removal left behind by an
// earlier failed Delete does not make later Deletes fail.
func TestBundleDeleteWithStaleAtomicPath(t *testing.T) {
	ctx, root, state := bundleDirs(t)

	b, err := NewBundle(ctx, root, state, "stale-atomic", nil)
	require.NoError(t, err)
	hidden := filepath.Join(filepath.Dir(b.Path), "."+b.ID)
	require.NoError(t, os.MkdirAll(filepath.Join(hidden, "rootfs"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(hidden, "rootfs", "x"), []byte("x"), 0600))

	require.NoError(t, b.Delete())
	assert.NoDirExists(t, b.Path)
	assert.NoDirExists(t, hidden)
}

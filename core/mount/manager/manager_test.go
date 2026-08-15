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

package manager

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/gc"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"

	bolt "go.etcd.io/bbolt"
	bolterr "go.etcd.io/bbolt/errors"
)

// absSource returns a platform-absolute path, suitable for use as a
// shareable mount's source (see shareable in backing.go) in a test
// which does not otherwise care what the path is, only that
// filepath.IsAbs agrees it is absolute. A Unix-style literal such as
// "/dev/null" is not: filepath.IsAbs on Windows requires a volume
// name, which a literal like that does not have, so a test which
// exercises dedup using one would silently exercise no sharing at all
// there.
func absSource(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	return "C:" + filepath.FromSlash(path)
}

// Fake, never-really-mounted sources shared across many tests in this
// file, each run through absSource so they remain absolute, and
// therefore shareable, on every platform.
var (
	testDevNull   = absSource("/dev/null")
	testDevZero   = absSource("/dev/zero")
	testSharedImg = absSource("/images/shared.img")
	testPodImg    = absSource("/images/pod.img")
)

func TestManager(t *testing.T) {
	testutil.RequiresRoot(t)
	td := t.TempDir()
	ctx := namespaces.WithNamespace(context.Background(), "test")

	sourcedir := filepath.Join(td, "source")
	if err := os.Mkdir(sourcedir, 0700); err != nil {
		t.Fatal(err)
	}
	mounts := []mount.Mount{
		{
			Type:    "bind",
			Source:  sourcedir,
			Options: []string{"rbind", "ro"},
		},
	}

	// newManager creates a fresh bolt DB and mount manager for each subtest so
	// that closing the manager (which now closes the DB) does not affect sibling
	// subtests.  It returns the manager and the target directory it was given.
	newManager := func(t *testing.T, opts ...Opt) (mount.Manager, string) {
		t.Helper()
		subtd := t.TempDir()
		db, err := bolt.Open(filepath.Join(subtd, "mounts.db"), 0600, nil)
		require.NoError(t, err)
		targetdir := filepath.Join(subtd, "m")
		m, err := NewManager(db, targetdir, opts...)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })
		return m, targetdir
	}

	t.Run("ActivateNoMounts", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.Activate(ctx, "id1", []mount.Mount{})
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOnly", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.Activate(ctx, "id1", mounts)
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOverride", func(t *testing.T) {
		m, targetdir := newManager(t, WithMountHandler("bind", &noopHandler{mounts: &atomic.Int32{}}))
		ainfo, err := m.Activate(ctx, "id1", mounts)
		require.NoError(t, err)
		defer assert.NoError(t, m.Deactivate(ctx, "id1"))

		assert.Equal(t, len(ainfo.Active), 1)
		assert.Equal(t, len(ainfo.System), 0)
		assert.Equal(t, ainfo.Active[0].Source, sourcedir)
		assert.Equal(t, ainfo.Active[0].Type, "bind")
		assert.Equal(t, ainfo.Active[0].MountPoint, filepath.Join(targetdir, backingDir, "2", mountPointName))
	})

	// try mounting
	// Test mount

}

// noopHandler is a Handler which does not touch the filesystem at
// all. Since realizeMount now checks liveness by asking the handler
// rather than by trusting its own bookkeeping, noopHandler tracks
// which paths it considers mounted itself and implements
// mount.MountedChecker to report them, so tests can still exercise
// dedup and reuse without a real mount underneath.
type noopHandler struct {
	mounts *atomic.Int32

	mu   sync.Mutex
	live map[string]struct{}

	// unmountAttempts, if set, counts every call to Unmount, whether
	// or not the path was known to be live, for a test which needs to
	// verify an unmount was attempted at all regardless of whether
	// there was ever anything real to undo.
	unmountAttempts *atomic.Int32
}

func (h *noopHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	now := time.Now()
	h.mounts.Add(1)
	h.mu.Lock()
	if h.live == nil {
		h.live = map[string]struct{}{}
	}
	h.live[mp] = struct{}{}
	h.mu.Unlock()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &now,
		MountPoint: mp,
	}, nil
}

// Unmount, like a real handler's, tolerates being asked to unmount a
// path it never actually mounted: it only counts down for one it
// knows about, matching the requirement that unmounting a mounted
// record which was resolved but never realized be a harmless no-op.
func (h *noopHandler) Unmount(_ context.Context, mp string) error {
	if h.unmountAttempts != nil {
		h.unmountAttempts.Add(1)
	}
	h.mu.Lock()
	_, wasLive := h.live[mp]
	delete(h.live, mp)
	h.mu.Unlock()
	if wasLive {
		h.mounts.Add(-1)
	}
	return nil
}

func (h *noopHandler) Mounted(_ context.Context, path string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.live[path]
	return ok, nil
}

type errOnceHandler struct {
	mounts  *atomic.Int32
	mounted map[string]struct{}
}

func (h *errOnceHandler) Mount(_ context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	h.mounted[mp] = struct{}{}
	h.mounts.Add(1)
	now := time.Now()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &now,
		MountPoint: mp,
		MountData:  nil,
	}, nil
}

func (h *errOnceHandler) Unmount(_ context.Context, mp string) error {
	if _, ok := h.mounted[mp]; ok {
		delete(h.mounted, mp)
		return fmt.Errorf("first unmount always fails")
	}
	h.mounts.Add(-1)
	return nil
}

// TestGC tests the garbage collecion features of the mount manager,
// ensuring that mounts are properly cleaned up when no longer needed.
func TestGC(t *testing.T) {
	type gcrun struct {
		a      []mount.Mount
		o      []mount.ActivateOpt
		d      []string
		all    []string
		active []string
		brefs  map[string][]string
		remove []string
		gcErr  bool
	}

	for _, tc := range []struct {
		name   string
		gcruns []gcrun
	}{
		{
			name: "Simple",
			gcruns: []gcrun{
				{
					a: []mount.Mount{
						{
							Type: "noop",
						},
					},
					all:    []string{"0-0"},
					remove: []string{},
				},
				{
					all:    []string{"0-0"},
					remove: []string{"0-0"},
				},
				{},
			},
		},
		{
			name: "UnmountError",
			gcruns: []gcrun{
				{
					a: []mount.Mount{
						{
							Type: "error",
						},
					},
					all:    []string{"0-0"},
					remove: []string{},
				},
				{
					all:    []string{"0-0"},
					remove: []string{"0-0"},
					gcErr:  true, // Expect an error on garbage collection due to unmount error
				},
				{}, // Run again without error to bring mount count back to zero
			},
		},
		{
			name: "ActiveBrefs",
			gcruns: []gcrun{
				{
					a: []mount.Mount{
						{
							Type: "noop",
						},
						{
							Type: "noop",
						},
					},
					o: []mount.ActivateOpt{
						mount.WithLabels(map[string]string{"containerd.io/gc.bref.container": "container1"}),
					},
					all: []string{"0-0", "0-1"},
					brefs: map[string][]string{
						"container1": {"0-0", "0-1"},
					},
					remove: []string{"0-1"},
				},
				{
					all:    []string{"0-0"},
					remove: []string{"0-0"},
					brefs: map[string][]string{
						"container1": {"0-0"},
					},
				},
				{
					brefs: map[string][]string{
						"container1": nil,
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			metadb := filepath.Join(td, "mounts.db")
			targetdir := filepath.Join(td, "m")
			db, err := bolt.Open(metadb, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := namespaces.WithNamespace(context.Background(), "test")

			sourcedir := filepath.Join(td, "source")
			if err := os.Mkdir(sourcedir, 0700); err != nil {
				t.Fatal(err)
			}
			mountC := new(atomic.Int32)
			m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}), WithMountHandler("error", &errOnceHandler{mounts: mountC, mounted: make(map[string]struct{})}))
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, m.(io.Closer).Close())
			})

			for i, run := range tc.gcruns {
				for j, mnt := range run.a {
					id := fmt.Sprintf("%d-%d", i, j)
					m.Activate(ctx, id, []mount.Mount{mnt}, run.o...)
				}

				for _, id := range run.d {
					if err := m.Deactivate(ctx, id); err != nil {
						t.Fatalf("deactivate %s: %v", id, err)
					}
				}

				cc, err := m.(interface {
					StartCollection(context.Context) (metadata.CollectionContext, error)
				}).StartCollection(ctx)
				require.NoError(t, err)

				var all []string

				checkGCActive(t, i, cc, run.active, run.brefs)

				cc.All(func(n gc.Node) {
					all = append(all, n.Key)
				})

				require.Equal(t, run.all, all, "run %d: all does not match", i)

				for _, id := range run.remove {
					cc.Remove(gc.Node{
						Type:      metadata.ResourceMount,
						Namespace: "test",
						Key:       id,
					})
				}

				err = cc.Finish()
				if run.gcErr && err == nil {
					t.Fatalf("expected error on run %d", i)
				} else if !run.gcErr && err != nil {
					t.Fatalf("unexpected error on run %d: %v", i, err)
				}

				// Interface functions not covered, cover in another test?
				// Active(namespace string, fn func(gc.Node))
				// Leased(namespace, lease string, fn func(gc.Node))
				// Cancel() error
			}
			if mountC.Load() != 0 {
				t.Fatalf("remaining mounts: %d", mountC.Load())
			}
		})
	}
}

func checkGCActive(t *testing.T, i int, cc metadata.CollectionContext, active []string, brefs map[string][]string) {
	t.Helper()
	ccb := cc.(interface {
		ActiveWithBackRefs(string, func(gc.Node), func(gc.Node, gc.Node))
	})

	var (
		activeKeys  []string
		activeBrefs = map[string][]string{}
	)
	ccb.ActiveWithBackRefs("test", func(n gc.Node) {
		activeKeys = append(activeKeys, n.Key)
	}, func(n, ref gc.Node) {
		activeBrefs[n.Key] = append(activeBrefs[n.Key], ref.Key)
	})

	require.Equal(t, active, activeKeys, "run %d: active does not match", i)

	for k := range brefs {
		require.Equal(t, brefs[k], activeBrefs[k], "run %d: brefs for %q does not match", i, k)
	}
}

func TestActivateAlreadyExists(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })

	mounts := []mount.Mount{{Type: "noop"}}

	// First activation should succeed
	_, err = m.Activate(ctx, "task1", mounts)
	require.NoError(t, err)

	// Second activation with same name should return ErrAlreadyExists
	_, err = m.Activate(ctx, "task1", mounts)
	assert.True(t, errdefs.IsAlreadyExists(err), "expected ErrAlreadyExists, got: %v", err)

	// Info should return valid info for the existing mount
	info, err := m.Info(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, "task1", info.Name)
	assert.Equal(t, 1, len(info.Active))

	// Cleanup
	assert.NoError(t, m.Deactivate(ctx, "task1"))
}

// TestActivateStaleIncomplete verifies that an activation whose chain
// was resolved but never realized, for example because the process
// died in between, is detected as stale and replaced, and that the
// mounted record it resolved, never having actually been mounted, is
// released.
func TestActivateStaleIncomplete(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	stale := mount.Mount{Type: "noop", Source: testDevZero}

	// Reproduce the crash window: the chain was resolved (the record
	// exists and this activation uses it) but the process died before
	// realizing it, so nothing was ever actually mounted.
	var staleMP string
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}
		nsbkt, err := v1bkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMounts)
		if err != nil {
			return err
		}
		if _, err := mbkt.CreateBucket([]byte("task1")); err != nil {
			return err
		}
		rec, err := resolvePosition(tx, mm.targets.Name(), "test", "task1", 0, stale, time.Now())
		if err != nil {
			return err
		}
		staleMP = rec.point
		return nil
	}))
	// The directory itself is created as part of realizing a mount;
	// simulate having gotten that far too, still without ever calling
	// the handler.
	require.NoError(t, mm.prepareRecordDir(staleMP, stale.Type, true))

	// Activating the same name must clean up the stale record.
	ainfo, err := m.Activate(ctx, "task1", []mount.Mount{{Type: "noop", Source: testDevNull}})
	require.NoError(t, err)
	assert.Equal(t, "task1", ainfo.Name)
	require.Equal(t, 1, len(ainfo.Active))

	assert.Equal(t, int32(1), mountC.Load(), "new mount made; stale record was never actually mounted")
	_, err = os.Stat(filepath.Dir(staleMP))
	assert.True(t, os.IsNotExist(err), "stale mounted record directory should be removed")

	assert.NoError(t, m.Deactivate(ctx, "task1"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestActivateStaleKeepsReferencedBackingMount verifies that replacing
// a stale activation does not tear down a mount another activation is
// using. A stale activation which shares every one of its records with
// a live activation is, by that fact alone, itself live (there is
// nothing left to repair), so this gives the stale activation an
// additional, unique record of its own which was never realized, to
// force it to be replaced.
func TestActivateStaleKeepsReferencedBackingMount(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	shared := mount.Mount{Type: "noop", Source: testDevNull}
	unique := mount.Mount{Type: "noop", Source: testDevZero}

	keep, err := m.Activate(ctx, "keep", []mount.Mount{shared})
	require.NoError(t, err)
	mp := keep.Active[0].MountPoint
	assert.Equal(t, int32(1), mountC.Load())

	// A stale activation which uses the same mount as "keep", plus a
	// second, unique mount of its own which was never realized.
	var staleUniqueMP string
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		mbkt := getBucket(tx, bucketKeyV2, []byte("test"), bucketKeyMounts)
		if _, err := mbkt.CreateBucket([]byte("task1")); err != nil {
			return err
		}
		if _, err := resolvePosition(tx, mm.targets.Name(), "test", "task1", 0, shared, time.Now()); err != nil {
			return err
		}
		rec, err := resolvePosition(tx, mm.targets.Name(), "test", "task1", 1, unique, time.Now())
		if err != nil {
			return err
		}
		staleUniqueMP = rec.point
		return nil
	}))
	require.NoError(t, mm.prepareRecordDir(staleUniqueMP, unique.Type, true))

	_, err = m.Activate(ctx, "task1", []mount.Mount{shared, unique})
	require.NoError(t, err)
	assert.Equal(t, int32(2), mountC.Load(), "shared mount must not be remounted; a mount is made for the unique one")
	_, err = os.Stat(filepath.Dir(mp))
	assert.NoError(t, err, "mount shared with another activation must survive stale cleanup")
	_, err = os.Stat(filepath.Dir(staleUniqueMP))
	assert.True(t, os.IsNotExist(err), "stale unique mounted record directory should be removed")

	require.NoError(t, m.Deactivate(ctx, "keep"))
	require.NoError(t, m.Deactivate(ctx, "task1"))
	assert.Equal(t, int32(0), mountC.Load())
}

func TestInfo(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })

	// Info on non-existent mount should return ErrNotFound
	_, err = m.Info(ctx, "nonexistent")
	assert.True(t, errdefs.IsNotFound(err), "expected ErrNotFound, got: %v", err)

	// Activate a mount with labels
	labels := map[string]string{
		"containerd.io/gc.bref.container": "ctr1",
		"custom-label":                    "value1",
	}
	mounts := []mount.Mount{{Type: "noop"}}
	ainfo, err := m.Activate(ctx, "task1", mounts, mount.WithLabels(labels))
	require.NoError(t, err)
	defer m.Deactivate(ctx, "task1")

	// Info should return the correct activation info
	info, err := m.Info(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, "task1", info.Name)
	assert.Equal(t, 1, len(info.Active))
	assert.Equal(t, "noop", info.Active[0].Type)
	assert.NotNil(t, info.Active[0].MountedAt)
	assert.Equal(t, labels, info.Labels)

	// Info active and system mounts should match those returned by Activate
	require.Equal(t, len(ainfo.Active), len(info.Active))
	for i := range ainfo.Active {
		assert.Equal(t, ainfo.Active[i].Type, info.Active[i].Type)
		assert.Equal(t, ainfo.Active[i].MountPoint, info.Active[i].MountPoint)
		assert.Equal(t, ainfo.Active[i].MountedAt.Unix(), info.Active[i].MountedAt.Unix())
	}
	// No system mounts when all mounts are handled
	assert.Empty(t, ainfo.System)
	assert.Empty(t, info.System)

	// Activate a second mount and verify Info returns correct data for each
	_, err = m.Activate(ctx, "task2", mounts)
	require.NoError(t, err)
	defer m.Deactivate(ctx, "task2")

	info2, err := m.Info(ctx, "task2")
	require.NoError(t, err)
	assert.Equal(t, "task2", info2.Name)
	assert.Equal(t, 1, len(info2.Active))
	// task2 has no custom labels
	assert.Empty(t, info2.Labels)

	// Original task1 info should be unchanged
	info1, err := m.Info(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, "task1", info1.Name)
	assert.Equal(t, labels, info1.Labels)
}

func TestInfoSystemMounts(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	// Only register a handler for "noop"; "bind" will pass through as a system mount
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })

	sourcedir := filepath.Join(td, "source")
	require.NoError(t, os.Mkdir(sourcedir, 0700))

	mounts := []mount.Mount{
		{Type: "noop"},
		{
			Type:    "bind",
			Source:  sourcedir,
			Options: []string{"rbind", "ro"},
		},
	}

	ainfo, err := m.Activate(ctx, "task1", mounts)
	require.NoError(t, err)
	defer m.Deactivate(ctx, "task1")

	// Activate should return one active mount and one system mount
	require.Equal(t, 1, len(ainfo.Active))
	require.Equal(t, 1, len(ainfo.System))
	assert.Equal(t, "bind", ainfo.System[0].Type)
	assert.Equal(t, sourcedir, ainfo.System[0].Source)
	assert.Equal(t, []string{"rbind", "ro"}, ainfo.System[0].Options)

	// Info should return the same active and system mounts
	info, err := m.Info(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, "task1", info.Name)

	require.Equal(t, len(ainfo.Active), len(info.Active))
	for i := range ainfo.Active {
		assert.Equal(t, ainfo.Active[i].Type, info.Active[i].Type)
		assert.Equal(t, ainfo.Active[i].MountPoint, info.Active[i].MountPoint)
		assert.Equal(t, ainfo.Active[i].MountedAt.Unix(), info.Active[i].MountedAt.Unix())
	}

	require.Equal(t, len(ainfo.System), len(info.System))
	for i := range ainfo.System {
		assert.Equal(t, ainfo.System[i].Type, info.System[i].Type)
		assert.Equal(t, ainfo.System[i].Source, info.System[i].Source)
		assert.Equal(t, ainfo.System[i].Target, info.System[i].Target)
		assert.Equal(t, ainfo.System[i].Options, info.System[i].Options)
	}
}

func TestActivateConcurrentSameName(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })

	mounts := []mount.Mount{{Type: "noop"}}

	// Launch two concurrent activations with the same name.
	// The per-name lock should serialize them: first succeeds,
	// second gets ErrAlreadyExists (not a stale-recovery race).
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := m.Activate(ctx, "task1", mounts)
			errs <- err
		}()
	}

	err1 := <-errs
	err2 := <-errs

	// Exactly one should succeed and one should get ErrAlreadyExists
	if err1 == nil && errdefs.IsAlreadyExists(err2) {
		// ok
	} else if err2 == nil && errdefs.IsAlreadyExists(err1) {
		// ok
	} else {
		t.Fatalf("expected one nil and one ErrAlreadyExists, got: %v, %v", err1, err2)
	}

	assert.NoError(t, m.Deactivate(ctx, "task1"))
}

// TODO: Test deactivate
// TODO: Test Sync

func TestClose(t *testing.T) {
	td := t.TempDir()
	db, err := bolt.Open(filepath.Join(td, "mounts.db"), 0600, nil)
	require.NoError(t, err)

	m, err := NewManager(db, filepath.Join(td, "m"))
	require.NoError(t, err)

	require.NoError(t, m.(io.Closer).Close())

	// Verify the underlying bolt DB is closed: a new transaction should fail.
	_, err = db.Begin(false)
	assert.ErrorIs(t, err, bolterr.ErrDatabaseNotOpen)
}

// mkTestManager builds a manager with the given handlers over a fresh
// bolt database.
func mkTestManager(t *testing.T, opts ...Opt) (mount.Manager, string) {
	t.Helper()
	td := t.TempDir()
	db, err := bolt.Open(filepath.Join(td, "mounts.db"), 0600, nil)
	require.NoError(t, err)
	targetdir := filepath.Join(td, "m")
	m, err := NewManager(db, targetdir, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })
	return m, targetdir
}

// TestBackingMount verifies that activations which describe the
// same mount resolve to a single mount, and that the mount survives
// until the last activation referencing it is deactivated.
func TestBackingMount(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("vol", &noopHandler{mounts: mountC}))

	vol := func() []mount.Mount {
		return []mount.Mount{{
			Type:    "vol",
			Source:  testDevNull,
			Options: []string{"rw"},
		}}
	}

	aInfo, err := m.Activate(ctx, "a", vol())
	require.NoError(t, err)
	require.Len(t, aInfo.Active, 1)
	mp := aInfo.Active[0].MountPoint
	require.NotEmpty(t, mp)
	assert.Equal(t, int32(1), mountC.Load(), "first activation mounts once")

	bInfo, err := m.Activate(ctx, "b", vol())
	require.NoError(t, err)
	require.Len(t, bInfo.Active, 1)
	assert.Equal(t, mp, bInfo.Active[0].MountPoint, "identical mount should be reused")
	assert.Equal(t, int32(1), mountC.Load(), "identical mount should not be mounted twice")

	// Info reports the same mount for both activations.
	for _, name := range []string{"a", "b"} {
		info, err := m.Info(ctx, name)
		require.NoError(t, err)
		require.Len(t, info.Active, 1)
		assert.Equal(t, mp, info.Active[0].MountPoint)
		assert.Equal(t, "vol", info.Active[0].Type)
		assert.Equal(t, testDevNull, info.Active[0].Source)
		assert.Equal(t, []string{"rw"}, info.Active[0].Options)
	}

	// Deactivating one activation must not disturb the other.
	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(1), mountC.Load(), "mount stays while b references it")
	_, err = os.Stat(filepath.Dir(mp))
	assert.NoError(t, err, "mount point must remain while referenced")

	require.NoError(t, m.Deactivate(ctx, "b"))
	assert.Equal(t, int32(0), mountC.Load(), "mount released after last reference")
	_, err = os.Stat(filepath.Dir(mp))
	assert.True(t, os.IsNotExist(err), "mount point should be removed with the last reference")
}

// TestBackingMountDistinctParameters verifies that mounts which
// differ in any parameter are not collapsed together.
func TestBackingMountDistinctParameters(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    mount.Mount
	}{
		{
			name: "Source",
			b:    mount.Mount{Type: "vol", Source: testDevZero, Options: []string{"rw"}},
		},
		{
			name: "Options",
			b:    mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"ro"}},
		},
		{
			name: "OptionOrder",
			b:    mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"nodev", "rw"}},
		},
		{
			name: "Target",
			b:    mount.Mount{Type: "vol", Source: testDevNull, Target: "/t", Options: []string{"rw", "nodev"}},
		},
		{
			name: "Type",
			b:    mount.Mount{Type: "vol2", Source: testDevNull, Options: []string{"rw", "nodev"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := namespaces.WithNamespace(context.Background(), "test")
			mountC := new(atomic.Int32)
			m, _ := mkTestManager(t,
				WithMountHandler("vol", &noopHandler{mounts: mountC}),
				WithMountHandler("vol2", &noopHandler{mounts: mountC}))

			a := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw", "nodev"}}

			aInfo, err := m.Activate(ctx, "a", []mount.Mount{a})
			require.NoError(t, err)
			bInfo, err := m.Activate(ctx, "b", []mount.Mount{tc.b})
			require.NoError(t, err)

			assert.NotEqual(t, aInfo.Active[0].MountPoint, bInfo.Active[0].MountPoint)
			assert.Equal(t, int32(2), mountC.Load(), "differing mounts must not be shared")

			require.NoError(t, m.Deactivate(ctx, "a"))
			require.NoError(t, m.Deactivate(ctx, "b"))
			assert.Equal(t, int32(0), mountC.Load())
		})
	}
}

// TestBackingMountSyntheticSource verifies that filesystems
// which synthesize their own contents are never shared. Two tmpfs
// mounts with identical parameters are still two distinct
// filesystems.
func TestBackingMountSyntheticSource(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("tmpfs", &noopHandler{mounts: mountC}))

	tmpfs := func() []mount.Mount {
		return []mount.Mount{{
			Type:    "tmpfs",
			Source:  "tmpfs",
			Options: []string{"size=1m"},
		}}
	}

	aInfo, err := m.Activate(ctx, "a", tmpfs())
	require.NoError(t, err)
	bInfo, err := m.Activate(ctx, "b", tmpfs())
	require.NoError(t, err)

	assert.NotEqual(t, aInfo.Active[0].MountPoint, bInfo.Active[0].MountPoint)
	assert.Equal(t, int32(2), mountC.Load(), "synthetic filesystems must not be shared")

	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(1), mountC.Load())
	require.NoError(t, m.Deactivate(ctx, "b"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestBackingMountNamespaces verifies that identical mounts in
// different namespaces are not shared.
func TestBackingMountNamespaces(t *testing.T) {
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("vol", &noopHandler{mounts: mountC}))

	vol := []mount.Mount{{Type: "vol", Source: testDevNull, Options: []string{"rw"}}}

	ctxA := namespaces.WithNamespace(context.Background(), "ns-a")
	ctxB := namespaces.WithNamespace(context.Background(), "ns-b")

	aInfo, err := m.Activate(ctxA, "a", vol)
	require.NoError(t, err)
	bInfo, err := m.Activate(ctxB, "a", vol)
	require.NoError(t, err)

	assert.NotEqual(t, aInfo.Active[0].MountPoint, bInfo.Active[0].MountPoint)
	assert.Equal(t, int32(2), mountC.Load(), "mounts must not be shared across namespaces")

	require.NoError(t, m.Deactivate(ctxA, "a"))
	assert.Equal(t, int32(1), mountC.Load())
	require.NoError(t, m.Deactivate(ctxB, "a"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestBackingMountTransformed verifies that the mount identity
// is computed from the mount as it is finally handed to the kernel,
// after any transforms have run, and that later mounts in a chain
// resolve their templates against the backing mount point.
//
// This is the shape produced by a block backed snapshotter: a backing
// image mount at the bottom of the chain with a per snapshot directory
// inside it.
func TestBackingMountTransformed(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t,
		WithMountHandler("blk", &noopHandler{mounts: mountC}),
		WithMountHandler("upper", &noopHandler{mounts: mountC}))

	chain := func(id string) []mount.Mount {
		return []mount.Mount{
			{
				Type:    "format/blk",
				Source:  testSharedImg,
				Options: []string{"rw", "loop"},
			},
			{
				Type:    "format/upper",
				Source:  "{{ mount 0 }}/upper-" + id,
				Options: []string{"rbind"},
			},
		}
	}

	aInfo, err := m.Activate(ctx, "a", chain("a"))
	require.NoError(t, err)
	require.Len(t, aInfo.Active, 2)
	blkMP := aInfo.Active[0].MountPoint
	assert.Equal(t, "blk", aInfo.Active[0].Type, "transform prefix must be peeled before mounting")
	assert.Equal(t, blkMP+"/upper-a", aInfo.Active[1].Source)
	assert.Equal(t, int32(2), mountC.Load())

	bInfo, err := m.Activate(ctx, "b", chain("b"))
	require.NoError(t, err)
	require.Len(t, bInfo.Active, 2)

	assert.Equal(t, blkMP, bInfo.Active[0].MountPoint,
		"identical transformed mount must be reused")
	assert.Equal(t, blkMP+"/upper-b", bInfo.Active[1].Source,
		"dependent mount must resolve against the backing mount point")
	assert.NotEqual(t, aInfo.Active[1].MountPoint, bInfo.Active[1].MountPoint,
		"differing upper mounts must not be shared")
	assert.Equal(t, int32(3), mountC.Load(), "only the upper mount is added")

	// The backing image stays mounted while either chain uses it.
	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(2), mountC.Load())
	_, err = os.Stat(filepath.Dir(blkMP))
	assert.NoError(t, err, "backing mount must survive while another chain uses it")

	require.NoError(t, m.Deactivate(ctx, "b"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestActivateRollbackReleasesBackingReferences verifies that a failed
// activation releases the references it took, without disturbing a
// mount another activation is still using.
func TestActivateRollbackReleasesBackingReferences(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t,
		WithMountHandler("vol", &noopHandler{mounts: mountC}),
		WithMountHandler("bad", &failHandler{}))

	vol := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw"}}

	okInfo, err := m.Activate(ctx, "ok", []mount.Mount{vol})
	require.NoError(t, err)
	mp := okInfo.Active[0].MountPoint
	assert.Equal(t, int32(1), mountC.Load())

	// A chain which reuses the same mount but then fails must leave
	// the backing mount alone.
	_, err = m.Activate(ctx, "fail", []mount.Mount{
		vol,
		{Type: "bad", Source: testDevZero},
	})
	require.Error(t, err)
	assert.Equal(t, int32(1), mountC.Load(), "backing mount must survive the rollback")
	_, err = os.Stat(filepath.Dir(mp))
	assert.NoError(t, err)

	// The failed activation must not be left behind.
	_, err = m.Info(ctx, "fail")
	assert.True(t, errdefs.IsNotFound(err), "expected ErrNotFound, got %v", err)

	// Retrying the same name works, so the previous references were
	// fully released.
	_, err = m.Activate(ctx, "fail", []mount.Mount{vol})
	require.NoError(t, err)
	assert.Equal(t, int32(1), mountC.Load())

	require.NoError(t, m.Deactivate(ctx, "ok"))
	require.NoError(t, m.Deactivate(ctx, "fail"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestActivateRollbackUnmountsUnreferencedBacking verifies that a failed
// activation unmounts the mounts it alone created.
func TestActivateRollbackUnmountsUnreferencedBacking(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t,
		WithMountHandler("vol", &noopHandler{mounts: mountC}),
		WithMountHandler("bad", &failHandler{}))

	_, err := m.Activate(ctx, "fail", []mount.Mount{
		{Type: "vol", Source: testDevNull, Options: []string{"rw"}},
		{Type: "bad", Source: testDevZero},
	})
	require.Error(t, err)
	assert.Equal(t, int32(0), mountC.Load(), "mounts made by the failed activation must be undone")
}

// failHandler returns an error on every mount, used to force an
// Activate failure in rollback tests.
type failHandler struct{}

func (failHandler) Mount(context.Context, mount.Mount, string, []mount.ActiveMount) (mount.ActiveMount, error) {
	return mount.ActiveMount{}, fmt.Errorf("forced failure")
}
func (failHandler) Unmount(context.Context, string) error { return nil }

// TestBackingMountGCRelease verifies that garbage collecting the
// activations which reference a backing mount releases it exactly once.
func TestBackingMountGCRelease(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("vol", &noopHandler{mounts: mountC}))

	vol := []mount.Mount{{Type: "vol", Source: testDevNull, Options: []string{"rw"}}}

	_, err := m.Activate(ctx, "a", vol)
	require.NoError(t, err)
	_, err = m.Activate(ctx, "b", vol)
	require.NoError(t, err)
	assert.Equal(t, int32(1), mountC.Load())

	collect := func(names ...string) {
		t.Helper()
		cc, err := m.(interface {
			StartCollection(context.Context) (metadata.CollectionContext, error)
		}).StartCollection(ctx)
		require.NoError(t, err)
		for _, n := range names {
			cc.Remove(gc.Node{Type: metadata.ResourceMount, Namespace: "test", Key: n})
		}
		require.NoError(t, cc.Finish())
	}

	// Removing one activation leaves the mount in place for the other.
	collect("a")
	assert.Equal(t, int32(1), mountC.Load(), "mount stays while b references it")

	collect("b")
	assert.Equal(t, int32(0), mountC.Load(), "mount released with the last reference")
}

// TestGCOrphanedBackingMount verifies that a mount directory left behind
// without a database record, as happens when the process dies between
// mounting and recording the mount, is unmounted by the collector.
func TestGCOrphanedBackingMount(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, targetdir := mkTestManager(t, WithMountHandler("vol", handler))

	// Simulate the crash: a mounted record's directory, with its type
	// file and mount point, left behind with no database record at
	// all, as if the process died between mounting and resolving it.
	// Mark it live directly on the handler, since it was never
	// mounted through the manager's own Activate flow.
	orphan := filepath.Join(targetdir, backingDir, "99")
	orphanMP := filepath.Join(orphan, mountPointName)
	require.NoError(t, os.MkdirAll(orphanMP, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, typeFileName), []byte("vol"), 0600))
	mountC.Add(1)
	handler.live = map[string]struct{}{orphanMP: {}}

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	require.NoError(t, cc.Finish())

	assert.Equal(t, int32(0), mountC.Load(), "orphaned mount should be unmounted with its handler")
	_, err = os.Stat(orphan)
	assert.True(t, os.IsNotExist(err), "orphaned backing mount directory should be removed")
}

// TestActivateConcurrentIdenticalMounts verifies that concurrent
// activations which resolve to the same mount perform the underlying
// mount exactly once.
func TestActivateConcurrentIdenticalMounts(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	total := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("vol", &countingHandler{active: mountC, total: total}))

	vol := []mount.Mount{{Type: "vol", Source: testDevNull, Options: []string{"rw"}}}

	const n = 8
	var wg sync.WaitGroup
	mps := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := m.Activate(ctx, fmt.Sprintf("task%d", i), vol)
			errs[i] = err
			if err == nil && len(info.Active) == 1 {
				mps[i] = info.Active[0].MountPoint
			}
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, mps[0], mps[i], "all activations should share one mount point")
	}
	assert.Equal(t, int32(1), total.Load(), "identical mount should be mounted exactly once")
	assert.Equal(t, int32(1), mountC.Load())

	for i := 0; i < n; i++ {
		require.NoError(t, m.Deactivate(ctx, fmt.Sprintf("task%d", i)))
	}
	assert.Equal(t, int32(0), mountC.Load())
}

// countingHandler tracks both currently active mounts and the total
// number of mount calls ever made. Like noopHandler, it implements
// mount.MountedChecker itself since it leaves nothing on the
// filesystem a generic check could ever find.
type countingHandler struct {
	active *atomic.Int32
	total  *atomic.Int32

	mu   sync.Mutex
	live map[string]struct{}
}

func (h *countingHandler) Mount(_ context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	now := time.Now()
	h.active.Add(1)
	h.total.Add(1)
	h.mu.Lock()
	if h.live == nil {
		h.live = map[string]struct{}{}
	}
	h.live[mp] = struct{}{}
	h.mu.Unlock()
	return mount.ActiveMount{Mount: m, MountedAt: &now, MountPoint: mp}, nil
}

// Unmount, like a real handler's, tolerates being asked to unmount a
// path it never actually mounted: it only counts down for one it
// knows about.
func (h *countingHandler) Unmount(_ context.Context, mp string) error {
	h.mu.Lock()
	_, wasLive := h.live[mp]
	delete(h.live, mp)
	h.mu.Unlock()
	if wasLive {
		h.active.Add(-1)
	}
	return nil
}

func (h *countingHandler) Mounted(_ context.Context, path string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.live[path]
	return ok, nil
}

// TestActivateAllMountsHandled covers a transformed chain in which every
// mount is handled by the manager, leaving no system mount to convert.
func TestActivateAllMountsHandled(t *testing.T) {
	td := t.TempDir()
	db, err := bolt.Open(filepath.Join(td, "mounts.db"), 0600, nil)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, filepath.Join(td, "m"),
		WithMountHandler("lower", &noopHandler{mounts: mountC}),
		WithMountHandler("upper", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.(io.Closer).Close()) })

	// Both mounts carry a transform and both have a handler, so
	// firstSystemMount is the length of the chain.
	ainfo, err := m.Activate(ctx, "a", []mount.Mount{
		{Type: "format/lower", Source: testDevNull},
		{Type: "format/upper", Source: "{{ mount 0 }}/child"},
	})
	require.NoError(t, err)
	require.Len(t, ainfo.Active, 2)
	assert.Empty(t, ainfo.System, "nothing is left for the system to mount")
	assert.Equal(t, ainfo.Active[0].MountPoint+"/child", ainfo.Active[1].Source,
		"the second mount should resolve against the first")

	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(0), mountC.Load())
}

// countingDB wraps a *bolt.DB, counting write transactions so a test
// can assert on how many Activate actually performs.
type countingDB struct {
	*bolt.DB
	writes atomic.Int32
}

func (c *countingDB) Update(fn func(*bolt.Tx) error) error {
	c.writes.Add(1)
	return c.DB.Update(fn)
}

// TestActivateSingleWriteTransaction verifies that Activate performs
// exactly one write transaction for a chain of managed mounts, no
// matter how many positions it has. This is the property the
// resolve-then-realize split exists to guarantee: a transaction per
// position would make activating a long chain serialize the whole
// database behind it once per position, which is unacceptable on a
// mount manager not backed by tmpfs, where a write transaction can be
// costly.
func TestActivateSingleWriteTransaction(t *testing.T) {
	td := t.TempDir()
	rawdb, err := bolt.Open(filepath.Join(td, "mounts.db"), 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { rawdb.Close() })

	mountC := new(atomic.Int32)
	m, err := NewManager(rawdb, filepath.Join(td, "m"),
		WithMountHandler("blk", &noopHandler{mounts: mountC}),
		WithMountHandler("upper", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })
	mm := m.(*mountManager)
	cdb := &countingDB{DB: rawdb}
	mm.db = cdb

	ctx := namespaces.WithNamespace(context.Background(), "test")
	_, err = m.Activate(ctx, "a", []mount.Mount{
		{Type: "format/blk", Source: testSharedImg, Options: []string{"rw", "loop"}},
		{Type: "format/upper", Source: "{{ mount 0 }}/upper", Options: []string{"rbind"}},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(1), cdb.writes.Load(), "Activate must perform exactly one write transaction")

	require.NoError(t, m.Deactivate(ctx, "a"))
}

// blockingHandler blocks inside Mount until release is closed, and
// signals entered once it does, so a test can force a deterministic
// window during which a concurrent activation resolving to the same
// identity must wait rather than mount a second time.
type blockingHandler struct {
	total       atomic.Int32
	entered     chan struct{}
	enteredOnce sync.Once
	release     chan struct{}

	mu   sync.Mutex
	live map[string]struct{}
}

func (h *blockingHandler) Mount(_ context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	h.total.Add(1)
	// Guarded by Once rather than called unconditionally: if a
	// regression ever let a second racer reach here, entered is
	// already closed, and Mount is exactly the call this test needs
	// to still observe failing, not one it should let take down the
	// whole test binary by closing an already-closed channel first.
	h.enteredOnce.Do(func() { close(h.entered) })
	<-h.release

	now := time.Now()
	h.mu.Lock()
	if h.live == nil {
		h.live = map[string]struct{}{}
	}
	h.live[mp] = struct{}{}
	h.mu.Unlock()
	return mount.ActiveMount{Mount: m, MountedAt: &now, MountPoint: mp}, nil
}

func (h *blockingHandler) Unmount(_ context.Context, mp string) error {
	h.mu.Lock()
	delete(h.live, mp)
	h.mu.Unlock()
	return nil
}

func (h *blockingHandler) Mounted(_ context.Context, path string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.live[path]
	return ok, nil
}

type activateResult struct {
	info mount.ActivationInfo
	err  error
}

// TestRealizeMountWaitsForConcurrentIdentity verifies that an
// activation which resolves to the same identity as one still in the
// middle of mounting waits for it rather than observing a
// not-yet-live record and mounting a second time.
func TestRealizeMountWaitsForConcurrentIdentity(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	handler := &blockingHandler{entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := mkTestManager(t, WithMountHandler("vol", handler))

	vol := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw"}}

	aDone := make(chan activateResult, 1)
	go func() {
		info, err := m.Activate(ctx, "a", []mount.Mount{vol})
		aDone <- activateResult{info, err}
	}()

	select {
	case <-handler.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first activation to start mounting")
	}

	// "b" resolves to the same identity while "a" is still inside
	// Mount. It must block acquiring the identity lock in
	// realizeMount, not proceed past a not-yet-live probe and mount
	// a second time.
	bDone := make(chan activateResult, 1)
	go func() {
		info, err := m.Activate(ctx, "b", []mount.Mount{vol})
		bDone <- activateResult{info, err}
	}()

	// There is no event to synchronize on for "b" reaching the lock;
	// give the scheduler a window. If "b" were not actually blocked,
	// the assertion on total mounts below would still catch it.
	time.Sleep(50 * time.Millisecond)

	close(handler.release)

	a := <-aDone
	require.NoError(t, a.err)
	b := <-bDone
	require.NoError(t, b.err)

	assert.Equal(t, int32(1), handler.total.Load(), "identical mount must be mounted exactly once")
	assert.Equal(t, a.info.Active[0].MountPoint, b.info.Active[0].MountPoint)

	require.NoError(t, m.Deactivate(ctx, "a"))
	require.NoError(t, m.Deactivate(ctx, "b"))
}

// recordUses returns the ids of the mounted records the named
// activation uses.
func recordUses(t *testing.T, mm *mountManager, namespace, name string) []uint64 {
	t.Helper()
	var ids []uint64
	require.NoError(t, mm.db.View(func(tx *bolt.Tx) error {
		bkt := getBucket(tx, bucketKeyV2, []byte(namespace), bucketKeyMounts, []byte(name))
		require.NotNil(t, bkt)
		ids = activationUses(bkt)
		return nil
	}))
	return ids
}

// TestRealizeMountRepairsInPlace verifies that a mounted record found
// not to be live, whether because it was never realized or because
// something outside this package tore it down, is repaired using its
// existing id and mount point rather than by minting a new one: one
// record must always mean one mount, never two ids describing what is
// really the same one.
func TestRealizeMountRepairsInPlace(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, _ := mkTestManager(t, WithMountHandler("vol", handler))
	mm := m.(*mountManager)

	vol := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw"}}

	aInfo, err := m.Activate(ctx, "a", []mount.Mount{vol})
	require.NoError(t, err)
	mp := aInfo.Active[0].MountPoint
	assert.Equal(t, int32(1), mountC.Load())
	idsBefore := recordUses(t, mm, "test", "a")
	require.Len(t, idsBefore, 1)

	// Something outside this package tears the mount down without
	// going through Deactivate: the manager's own bookkeeping still
	// describes it, but it is no longer actually there.
	handler.mu.Lock()
	delete(handler.live, mp)
	handler.mu.Unlock()
	mountC.Add(-1)

	// A second activation resolving to the same identity must find it
	// is not live and repair it in place.
	bInfo, err := m.Activate(ctx, "b", []mount.Mount{vol})
	require.NoError(t, err)
	assert.Equal(t, mp, bInfo.Active[0].MountPoint, "repair must reuse the same mount point, not allocate a new one")
	assert.Equal(t, int32(1), mountC.Load(), "repair mounts exactly once")

	idsAfter := recordUses(t, mm, "test", "b")
	require.Len(t, idsAfter, 1)
	assert.Equal(t, idsBefore[0], idsAfter[0], "repair must reuse the existing record id, never mint a new one")

	// "a", having never been told anything changed, now correctly
	// reports the record as live again too, since it was repaired at
	// the same path under the same id.
	info, err := m.Info(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, mp, info.Active[0].MountPoint)

	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(1), mountC.Load(), "mount stays while b references it")
	require.NoError(t, m.Deactivate(ctx, "b"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestPodGroupSharesOneMount mirrors the shape a block backed
// snapshotter uses to share one filesystem between every container of
// a pod: a shared image mount at the bottom of each container's
// chain, and a container-unique directory bind mount stacked on top
// of it. It verifies the image is mounted exactly once no matter how
// many containers reference it, which is the property pod-level
// sharing depends on.
func TestPodGroupSharesOneMount(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t,
		WithMountHandler("image", &noopHandler{mounts: mountC}),
		WithMountHandler("dir", &noopHandler{mounts: mountC}))

	chain := func(container string) []mount.Mount {
		return []mount.Mount{
			{
				Type:    "format/image",
				Source:  testPodImg,
				Options: []string{"rw", "loop"},
			},
			{
				Type:    "format/dir",
				Source:  "{{ mount 0 }}/upper-" + container,
				Options: []string{"rbind"},
			},
		}
	}

	const n = 3
	var mps []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("container%d", i)
		info, err := m.Activate(ctx, name, chain(name))
		require.NoError(t, err)
		require.Len(t, info.Active, 2)
		mps = append(mps, info.Active[0].MountPoint)
		assert.Equal(t, info.Active[0].MountPoint+"/upper-"+name, info.Active[1].Source)
	}

	for i := 1; i < n; i++ {
		assert.Equal(t, mps[0], mps[i], "every container must share the same image mount point")
	}
	assert.Equal(t, int32(1+n), mountC.Load(), "one shared image mount, plus one unique directory mount per container")

	for i := 0; i < n; i++ {
		require.NoError(t, m.Deactivate(ctx, fmt.Sprintf("container%d", i)))
	}
	assert.Equal(t, int32(0), mountC.Load())
}

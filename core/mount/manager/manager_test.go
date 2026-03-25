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
)

func TestManager(t *testing.T) {
	testutil.RequiresRoot(t)
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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

	t.Run("ActivateNoMounts", func(t *testing.T) {
		m, err := NewManager(db, targetdir)
		require.NoError(t, err)
		t.Cleanup(func() { m.(io.Closer).Close() })
		_, err = m.Activate(ctx, "id1", []mount.Mount{})
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOnly", func(t *testing.T) {
		m, err := NewManager(db, targetdir)
		require.NoError(t, err)
		t.Cleanup(func() { m.(io.Closer).Close() })
		_, err = m.Activate(ctx, "id1", mounts)
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOverride", func(t *testing.T) {
		m, err := NewManager(db, targetdir, WithMountHandler("bind", &noopHandler{mounts: &atomic.Int32{}}))
		require.NoError(t, err)
		t.Cleanup(func() { m.(io.Closer).Close() })
		ainfo, err := m.Activate(ctx, "id1", mounts)
		require.NoError(t, err)
		defer assert.NoError(t, m.Deactivate(ctx, "id1"))

		assert.Equal(t, len(ainfo.Active), 1)
		assert.Equal(t, len(ainfo.System), 0)
		assert.Equal(t, ainfo.Active[0].Source, sourcedir)
		assert.Equal(t, ainfo.Active[0].Type, "bind")
		assert.Equal(t, ainfo.Active[0].MountPoint, filepath.Join(targetdir, "1", "1"))
	})

	// try mounting
	// Test mount

}

type noopHandler struct {
	mounts *atomic.Int32
}

func (h *noopHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	now := time.Now()
	h.mounts.Add(1)
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &now,
		MountPoint: mp,
	}, nil
}

func (h *noopHandler) Unmount(context.Context, string) error {
	h.mounts.Add(-1)
	return nil
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
			defer db.Close()
			ctx := namespaces.WithNamespace(context.Background(), "test")

			sourcedir := filepath.Join(td, "source")
			if err := os.Mkdir(sourcedir, 0700); err != nil {
				t.Fatal(err)
			}
			mountC := new(atomic.Int32)
			m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}), WithMountHandler("error", &errOnceHandler{mounts: mountC, mounted: make(map[string]struct{})}))
			require.NoError(t, err)
			t.Cleanup(func() { m.(io.Closer).Close() })

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
	t.Cleanup(func() { db.Close() })
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })

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

func TestActivateStaleIncomplete(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	ctx := namespaces.WithNamespace(context.Background(), "test")

	// Simulate a stale incomplete activation by directly writing a bucket
	// without the "active" sub-bucket (as if the process crashed mid-activation).
	// Use mount ID 42 so we can verify the stale target directory gets cleaned up.
	var staleMID uint64 = 42
	err = db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists([]byte("v1"))
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
		// Create the mount bucket but don't add the "active" sub-bucket
		bkt, err := mbkt.CreateBucket([]byte("task1"))
		if err != nil {
			return err
		}
		idb, err := encodeID(staleMID)
		if err != nil {
			return err
		}
		return bkt.Put(bucketKeyID, idb)
	})
	require.NoError(t, err)

	// Create the stale target directory as if the process crashed after
	// mkdir but before the second transaction.
	staleTarget := filepath.Join(targetdir, fmt.Sprintf("%d", staleMID))
	require.NoError(t, os.MkdirAll(staleTarget, 0700))

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })

	mounts := []mount.Mount{{Type: "noop"}}

	// Activation should succeed by cleaning up the stale entry
	ainfo, err := m.Activate(ctx, "task1", mounts)
	require.NoError(t, err)
	assert.Equal(t, "task1", ainfo.Name)
	assert.Equal(t, 1, len(ainfo.Active))

	// The stale target directory should have been cleaned up
	_, err = os.Stat(staleTarget)
	assert.True(t, os.IsNotExist(err), "stale target directory should be removed, but still exists")

	// Cleanup
	assert.NoError(t, m.Deactivate(ctx, "task1"))
}

func TestInfo(t *testing.T) {
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })

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
	t.Cleanup(func() { db.Close() })
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	// Only register a handler for "noop"; "bind" will pass through as a system mount
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })

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
	t.Cleanup(func() { db.Close() })
	ctx := namespaces.WithNamespace(context.Background(), "test")

	mountC := new(atomic.Int32)
	m, err := NewManager(db, targetdir, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	require.NoError(t, err)
	t.Cleanup(func() { m.(io.Closer).Close() })

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

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

// TODO: Test Info
// TODO: Test deactivate
// TODO: Test Sync

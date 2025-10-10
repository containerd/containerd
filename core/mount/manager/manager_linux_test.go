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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/containerd/log/logtest"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

func TestLoopbackMount(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}

	a := fstest.Apply(
		fstest.CreateFile("/foo", []byte("foo\n"), 0777),
		fstest.CreateDir("/a", 0755),
		fstest.CreateDir("/a/b", 0755),
		fstest.CreateDir("/a/b/c", 0755),
	)

	source, err := initalizeBlockDevice(td, a)
	if err != nil {
		t.Fatal(err)
	}
	mounts := []mount.Mount{
		{
			Type:    "loop",
			Source:  source,
			Options: []string{},
		},
		{
			Type:    "format/xfs",
			Source:  "{{ mount 0 }}", // previous mount
			Options: []string{},
		},
	}

	handlers := map[string]mount.Handler{
		"loop": mount.LoopbackHandler(),
	}
	m := NewManager(db, targetdir, handlers)
	ainfo, err := m.Activate(ctx, "id1", mounts)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, m.Deactivate(ctx, "id1"))
	}()

	tm, err := os.MkdirTemp(td, "test-mount-")
	if err != nil {
		t.Fatal(err)
	}

	actual, err := os.MkdirTemp(td, "actual-")
	if err != nil {
		t.Fatal(err)
	}

	if err := a.Apply(actual); err != nil {
		t.Fatal(err)
	}

	if err := mount.All(ainfo.System, tm); err != nil {
		t.Fatalf("failed to mount %v: %v", ainfo.System, err)
	}
	defer testutil.Unmount(t, tm)

	if err := fstest.CheckDirectoryEqual(tm, actual); err != nil {
		t.Fatalf("check directory failed: %v", err)
	}
}

func TestLoopbackOverlay(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}

	l1 := fstest.Apply(
		fstest.CreateFile("/foo", []byte("foo\n"), 0777),
		fstest.CreateDir("/a", 0755),
		fstest.CreateDir("/a/b", 0755),
		fstest.CreateDir("/a/b/c", 0755),
	)

	l2 := fstest.Apply(
		fstest.CreateFile("/foo", []byte("foo\n"), 0777),
		fstest.CreateDir("/a", 0755),
		fstest.CreateDir("/a/b", 0755),
		fstest.CreateDir("/a/b/c", 0755),
	)

	for _, tc := range []struct {
		name   string
		mounts func() ([]mount.Mount, error)
	}{
		{
			name: "LoopOptions",
			mounts: func() ([]mount.Mount, error) {
				b1, err := initalizeBlockDevice(td, l1)
				if err != nil {
					return nil, err
				}
				b2, err := initalizeBlockDevice(td, l2)
				if err != nil {
					return nil, err
				}
				return []mount.Mount{
					{
						Type:    "xfs",
						Source:  b1,
						Options: []string{"loop"},
					},
					{
						Type:    "xfs",
						Source:  b2,
						Options: []string{"loop"},
					},
					{
						Type:   "format/overlay",
						Source: "overlay",
						Options: []string{
							"lowerdir={{ mount 1 }}:{{ mount 0 }}",
						},
					},
				}, nil
			},
		},
		{
			name: "SeparateLoop",
			mounts: func() ([]mount.Mount, error) {
				b1, err := initalizeBlockDevice(td, l1)
				if err != nil {
					return nil, err
				}
				b2, err := initalizeBlockDevice(td, l2)
				if err != nil {
					return nil, err
				}
				return []mount.Mount{
					{
						Type:    "loop",
						Source:  b1,
						Options: []string{},
					},
					{
						Type:    "format/xfs",
						Source:  "{{ mount 0 }}",
						Options: []string{},
					},
					{
						Type:    "loop",
						Source:  b2,
						Options: []string{},
					},
					{
						Type:    "format/xfs",
						Source:  "{{ mount 2 }}",
						Options: []string{},
					},
					{
						Type:   "format/overlay",
						Source: "overlay",
						Options: []string{
							"lowerdir={{ mount 3 }}:{{ mount 1 }}",
						},
					},
				}, nil
			},
		},
		{
			name: "OverlayFunc",
			mounts: func() ([]mount.Mount, error) {
				b1, err := initalizeBlockDevice(td, l1)
				if err != nil {
					return nil, err
				}
				b2, err := initalizeBlockDevice(td, l2)
				if err != nil {
					return nil, err
				}
				return []mount.Mount{
					{
						Type:    "xfs",
						Source:  b1,
						Options: []string{"loop"},
					},
					{
						Type:    "xfs",
						Source:  b2,
						Options: []string{"loop"},
					},
					{
						Type:   "format/overlay",
						Source: "overlay",
						Options: []string{
							"lowerdir={{ overlay 1 0 }}",
						},
					},
				}, nil
			},
		},
		{
			name: "OverlayFuncReversed",
			mounts: func() ([]mount.Mount, error) {
				b1, err := initalizeBlockDevice(td, l1)
				if err != nil {
					return nil, err
				}
				b2, err := initalizeBlockDevice(td, l2)
				if err != nil {
					return nil, err
				}
				return []mount.Mount{
					{
						Type:    "xfs",
						Source:  b2,
						Options: []string{"loop"},
					},
					{
						Type:    "xfs",
						Source:  b1,
						Options: []string{"loop"},
					},
					{
						Type:   "format/overlay",
						Source: "overlay",
						Options: []string{
							"lowerdir={{ overlay 0 1 }}",
						},
					},
				}, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {

			mounts, err := tc.mounts()
			if err != nil {
				t.Fatal(err)
			}

			handlers := map[string]mount.Handler{
				"loop": mount.LoopbackHandler(),
			}
			m := NewManager(db, targetdir, handlers)
			ainfo, err := m.Activate(ctx, "id1", mounts)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, m.Deactivate(ctx, "id1"))
			}()

			tm, err := os.MkdirTemp(td, "test-mount-")
			if err != nil {
				t.Fatal(err)
			}

			actual, err := os.MkdirTemp(td, "actual-")
			if err != nil {
				t.Fatal(err)
			}

			if err := fstest.Apply(l1, l2).Apply(actual); err != nil {
				t.Fatal(err)
			}

			if err := mount.All(ainfo.System, tm); err != nil {
				t.Fatalf("failed to mount %v: %v", ainfo.System, err)
			}
			defer testutil.Unmount(t, tm)

			if err := fstest.CheckDirectoryEqual(tm, actual); err != nil {
				t.Fatalf("check directory failed: %v", err)
			}
		})
	}
}

func initalizeBlockDevice(td string, a fstest.Applier) (string, error) {
	file, err := os.CreateTemp(td, "fs-")
	if err != nil {
		return "", err
	}

	if err := file.Truncate(300 << 20); err != nil {
		file.Close()
		return "", fmt.Errorf("failed to resize loopback file: %w", err)
	}
	file.Close()

	loopdev, err := mount.AttachLoopDevice(file.Name())
	if err != nil {
		return "", err
	}
	defer mount.DetachLoopDevice(loopdev)

	if out, err := exec.Command("mkfs.xfs", loopdev).CombinedOutput(); err != nil {
		return "", fmt.Errorf("could not mkfs.xfs %s: %w (out: %s)", loopdev, err, string(out))
	}

	m := mount.Mount{
		Type:    "xfs",
		Source:  loopdev, // previous mount
		Options: []string{},
	}
	target, err := os.MkdirTemp(td, "mount-")
	if err != nil {
		return "", err
	}

	if err := m.Mount(target); err != nil {
		return "", err
	}
	defer mount.Unmount(target, 0)

	if err := a.Apply(target); err != nil {
		return "", err
	}

	return file.Name(), nil
}

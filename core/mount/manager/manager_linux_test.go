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
			Type:    "format/ext4",
			Source:  "{{ mount 0 }}", // previous mount
			Options: []string{},
		},
	}

	m, err := NewManager(db, targetdir, WithMountHandler("loop", mount.LoopbackHandler()))
	require.NoError(t, err)
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

	if err := fstest.CheckDirectoryEqual(filepath.Join(tm, "root"), actual); err != nil {
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
						Type:    "ext4",
						Source:  b1,
						Options: []string{"loop"},
					},
					{
						Type:    "ext4",
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
						Type:    "format/ext4",
						Source:  "{{ mount 0 }}",
						Options: []string{},
					},
					{
						Type:    "loop",
						Source:  b2,
						Options: []string{},
					},
					{
						Type:    "format/ext4",
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
						Type:    "ext4",
						Source:  b1,
						Options: []string{"loop"},
					},
					{
						Type:    "ext4",
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
						Type:    "ext4",
						Source:  b2,
						Options: []string{"loop"},
					},
					{
						Type:    "ext4",
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

			m, err := NewManager(db, targetdir, WithMountHandler("loop", mount.LoopbackHandler()))
			require.NoError(t, err)
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

			if err := fstest.CheckDirectoryEqual(filepath.Join(tm, "root"), actual); err != nil {
				t.Fatalf("check directory failed: %v", err)
			}
		})
	}
}

// TestTemporaryMountActivation tests the WithTemporary option used by
// `ctr images mount`. This verifies the bind mount returned in info.System
// points to a valid, mounted directory.
func TestTemporaryMountActivation(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	defer db.Close()

	sourcedir := filepath.Join(td, "source")
	require.NoError(t, os.MkdirAll(sourcedir, 0755))

	a := fstest.Apply(
		fstest.CreateFile("/testfile.txt", []byte("test content\n"), 0644),
		fstest.CreateDir("/testdir", 0755),
	)
	require.NoError(t, a.Apply(sourcedir))

	mounts := []mount.Mount{
		{
			Type:    "bind",
			Source:  sourcedir,
			Options: []string{"rbind", "ro"},
		},
	}

	m, err := NewManager(db, targetdir)
	require.NoError(t, err)

	ainfo, err := m.Activate(ctx, "temp-mount-test", mounts, mount.WithTemporary)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, m.Deactivate(ctx, "temp-mount-test"))
	}()

	require.NotEmpty(t, ainfo.System, "Expected system mounts to be returned")

	require.Len(t, ainfo.System, 1, "Expected exactly one system mount")
	systemMount := ainfo.System[0]
	assert.Equal(t, "bind", systemMount.Type, "Expected bind mount type")

	sourceInfo, err := os.Stat(systemMount.Source)
	require.NoError(t, err, "Bind mount source %q should exist", systemMount.Source)
	assert.True(t, sourceInfo.IsDir(), "Bind mount source should be a directory")

	testFile := filepath.Join(systemMount.Source, "testfile.txt")
	content, err := os.ReadFile(testFile)
	require.NoError(t, err, "Should be able to read test file from bind mount source")
	assert.Equal(t, "test content\n", string(content), "Test file content should match")

	targetMount := filepath.Join(td, "target")
	require.NoError(t, os.MkdirAll(targetMount, 0755))

	err = mount.All(ainfo.System, targetMount)
	require.NoError(t, err, "Should be able to mount system mounts to target")
	defer testutil.Unmount(t, targetMount)

	targetTestFile := filepath.Join(targetMount, "testfile.txt")
	targetContent, err := os.ReadFile(targetTestFile)
	require.NoError(t, err, "Should be able to read test file from target mount")
	assert.Equal(t, "test content\n", string(targetContent), "Target test file content should match")
}

// TestTemporaryOverlayMountActivation tests WithTemporary with overlay mounts,
// which is the more common case for `ctr images mount` with overlay snapshotter.
func TestTemporaryOverlayMountActivation(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	defer db.Close()

	lower1 := filepath.Join(td, "lower1")
	lower2 := filepath.Join(td, "lower2")
	upper := filepath.Join(td, "upper")
	work := filepath.Join(td, "work")

	require.NoError(t, os.MkdirAll(lower1, 0755))
	require.NoError(t, os.MkdirAll(lower2, 0755))
	require.NoError(t, os.MkdirAll(upper, 0755))
	require.NoError(t, os.MkdirAll(work, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(lower1, "file1.txt"), []byte("layer1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(lower2, "file2.txt"), []byte("layer2\n"), 0644))

	mounts := []mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				fmt.Sprintf("lowerdir=%s:%s", lower2, lower1),
				fmt.Sprintf("upperdir=%s", upper),
				fmt.Sprintf("workdir=%s", work),
			},
		},
	}

	m, err := NewManager(db, targetdir)
	require.NoError(t, err)

	ainfo, err := m.Activate(ctx, "temp-overlay-test", mounts, mount.WithTemporary)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, m.Deactivate(ctx, "temp-overlay-test"))
	}()

	require.NotEmpty(t, ainfo.System, "Expected system mounts to be returned")

	require.Len(t, ainfo.System, 1, "Expected exactly one system mount")
	systemMount := ainfo.System[0]
	assert.Equal(t, "bind", systemMount.Type, "Expected bind mount type")

	sourceInfo, err := os.Stat(systemMount.Source)
	require.NoError(t, err, "Bind mount source %q should exist", systemMount.Source)
	assert.True(t, sourceInfo.IsDir(), "Bind mount source should be a directory")

	file1 := filepath.Join(systemMount.Source, "file1.txt")
	file2 := filepath.Join(systemMount.Source, "file2.txt")

	content1, err := os.ReadFile(file1)
	require.NoError(t, err, "Should be able to read file1 from overlay via bind source")
	assert.Equal(t, "layer1\n", string(content1))

	content2, err := os.ReadFile(file2)
	require.NoError(t, err, "Should be able to read file2 from overlay via bind source")
	assert.Equal(t, "layer2\n", string(content2))

	targetMount := filepath.Join(td, "target")
	require.NoError(t, os.MkdirAll(targetMount, 0755))

	err = mount.All(ainfo.System, targetMount)
	require.NoError(t, err, "Should be able to mount system mounts to target")
	defer testutil.Unmount(t, targetMount)

	targetFile1 := filepath.Join(targetMount, "file1.txt")
	targetFile2 := filepath.Join(targetMount, "file2.txt")

	targetContent1, err := os.ReadFile(targetFile1)
	require.NoError(t, err)
	assert.Equal(t, "layer1\n", string(targetContent1))

	targetContent2, err := os.ReadFile(targetFile2)
	require.NoError(t, err)
	assert.Equal(t, "layer2\n", string(targetContent2))
}

func initalizeBlockDevice(td string, a fstest.Applier) (string, error) {
	file, err := os.CreateTemp(td, "fs-")
	if err != nil {
		return "", err
	}

	if err := file.Truncate(16 << 20); err != nil {
		file.Close()
		return "", fmt.Errorf("failed to resize loopback file: %w", err)
	}
	dpath := file.Name()
	file.Close()

	if out, err := exec.Command("mkfs.ext4", dpath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("could not mkfs.ext4 %s: %w (out: %s)", dpath, err, string(out))
	}

	m := mount.Mount{
		Type:    "ext4",
		Source:  dpath,
		Options: []string{"loop"},
	}
	target, err := os.MkdirTemp(td, "mount-")
	if err != nil {
		return "", err
	}

	if err := m.Mount(target); err != nil {
		return "", fmt.Errorf("failed to mount: %w", err)
	}

	rootDir := filepath.Join(target, "root")
	if err := os.Mkdir(rootDir, 0755); err != nil {
		return "", err
	}
	if err := a.Apply(rootDir); err != nil {
		mount.Unmount(target, 0)
		return "", err
	}

	if err := mount.Unmount(target, 0); err != nil {
		return "", fmt.Errorf("failed to unmount: %w", err)
	}

	return dpath, nil
}

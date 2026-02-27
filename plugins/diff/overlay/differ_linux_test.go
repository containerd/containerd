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

package overlay

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
)

func TestGetUpperDir(t *testing.T) {
	tests := []struct {
		name     string
		mounts   []mount.Mount
		expected string
	}{
		{
			name:     "empty mounts",
			mounts:   []mount.Mount{},
			expected: "",
		},
		{
			name: "non-overlay mount",
			mounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  "/some/path",
					Options: []string{"rw"},
				},
			},
			expected: "",
		},
		{
			name: "overlay mount with upperdir",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower",
						"upperdir=/upper/diff",
						"workdir=/upper/work",
					},
				},
			},
			expected: "/upper/diff",
		},
		{
			name: "overlay mount without upperdir (read-only)",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower1:/lower2",
					},
				},
			},
			expected: "",
		},
		{
			name: "multiple mounts with overlay last",
			mounts: []mount.Mount{
				{
					Type:   "bind",
					Source: "/bind/source",
				},
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/lower",
						"upperdir=/data/upper",
						"workdir=/data/work",
					},
				},
			},
			expected: "/data/upper",
		},
		{
			name: "multiple mounts with non-overlay last",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"upperdir=/upper",
					},
				},
				{
					Type:   "bind",
					Source: "/bind/source",
				},
			},
			expected: "",
		},
		{
			name: "overlay with complex real-world options",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs",
						"upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
						"workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/work",
					},
				},
			},
			expected: "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getUpperDir(tt.mounts)
			if result != tt.expected {
				t.Errorf("getUpperDir() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// BenchmarkGetUpperDir measures the cost of parsing overlay mount options
// to extract the upperdir. This is called on every Compare invocation.
func BenchmarkGetUpperDir(b *testing.B) {
	mounts := []mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"index=off",
				"lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs",
				"upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs",
				"workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/2/work",
			},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getUpperDir(mounts)
	}
}

// createTestTree creates a directory tree with numFiles regular files and
// returns its path. Files are small but numerous to reflect realistic image
// layer structures.
func createTestTree(t testing.TB, dir string, numFiles int) {
	t.Helper()
	for i := 0; i < numFiles; i++ {
		subdir := filepath.Join(dir, "subdir", filepath.FromSlash("a/b"))
		if err := os.MkdirAll(subdir, 0755); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(subdir, "file")
		if i > 0 {
			name = filepath.Join(dir, filepath.FromSlash("subdir"), filepath.FromSlash("a/b"),
				string(rune('a'+i%26))+string(rune('a'+(i/26)%26)))
		}
		if err := os.WriteFile(name, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// BenchmarkSingleWalkUpperDir benchmarks the overlay differ's approach:
// writing a diff by walking only the upperdir. This simulates the overlay
// fast-path where only a handful of files changed on top of a large lower layer.
//
// Compare with BenchmarkDoubleWalkDiff which walks both lower and upper dirs.
func BenchmarkSingleWalkUpperDir(b *testing.B) {
	// Create a "lower" tree with many files (simulating a large base image layer).
	// In the overlay fast-path we never traverse these files.
	lowerDir := b.TempDir()
	createTestTree(b, lowerDir, 200)

	// Create an "upper" tree with a few changed files (simulating a thin layer).
	upperDir := b.TempDir()
	createTestTree(b, upperDir, 10)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cw := archive.NewChangeWriter(io.Discard, upperDir)
		// Walk only the upperdir; lowerDir is referenced but not traversed here.
		// In production this uses fs.DiffDirChanges + DiffSourceOverlayFS which
		// reads overlay xattrs. Here we use filepath.Walk for pure I/O comparison.
		err := filepath.Walk(upperDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		_ = cw
		_ = lowerDir // referenced but intentionally not walked
	}
}

// BenchmarkDoubleWalkDiff benchmarks the naive double-walk approach used by
// the walking differ: it traverses both lower and upper directory trees to
// detect changes.
//
// With a large lower layer this is significantly slower than BenchmarkSingleWalkUpperDir
// even though the number of changed files is the same.
func BenchmarkDoubleWalkDiff(b *testing.B) {
	lowerDir := b.TempDir()
	createTestTree(b, lowerDir, 200)

	upperDir := b.TempDir()
	createTestTree(b, upperDir, 10)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Simulate the double-walk: both lower and upper must be traversed.
		for _, root := range []string{lowerDir, upperDir} {
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

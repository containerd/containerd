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
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
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
			expected: "",
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

		{
			name: "overlay targeted at subdirectory",
			mounts: []mount.Mount{{
				Type: "overlay",
				Target: "/subdir",
				Options: []string{"upperdir=/upper"},
			}},
			expected: "",
		},
		{
			name: "overlay targeted at root",
			mounts: []mount.Mount{{
				Type: "overlay",
				Target: "/",
				Options: []string{"upperdir=/upper"},
			}},
			expected: "/upper",
		},
		{
			name: "comma-joined overlay options",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper,workdir=/work"},
			}},
			expected: "",
		},
		{
			name: "kernel escaped overlay path",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{`upperdir=/hello\\040world`},
			}},
			expected: "",
		},
		{
			name: "metacopy enabled",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/upper", "metacopy=on"},
			}},
			expected: "",
		},
		{
			name: "duplicate upperdir options",
			mounts: []mount.Mount{{
				Type: "overlay",
				Options: []string{"upperdir=/a", "upperdir=/b"},
			}},
			expected: "",
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


// TestOverlayLowerMatches verifies that an overlay upperdir is never used as
// a diff against a different base snapshot.
func TestOverlayLowerMatches(t *testing.T) {
	tests := []struct {
		name  string
		lower []mount.Mount
		upper []mount.Mount
		want  bool
	}{
		{
			name:  "matching bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
			want:  true,
		},
		{
			name:  "unrelated bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/different"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "matching readonly overlay",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two", "upperdir=/layers/three"}}},
			want:  true,
		},
		{
			name:  "different overlay lower stack",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/other"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/three"}}},
		},
		{
			name:  "mutable overlay lower",
			lower: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/other"}}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/three"}}},
		},
		{
			name:  "empty base",
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "subdirectory bind",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one", Target: "/subdir"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one", "upperdir=/layers/two"}}},
		},
		{
			name:  "multi-layer upper versus bind lower",
			lower: []mount.Mount{{Type: "bind", Source: "/layers/one"}},
			upper: []mount.Mount{{Type: "overlay", Options: []string{"lowerdir=/layers/one:/layers/two", "upperdir=/layers/three"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overlayLowerMatches(tt.lower, tt.upper); got != tt.want {
				t.Fatalf("overlayLowerMatches() = %v, want %v", got, tt.want)
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


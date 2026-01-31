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

package walking

import (
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestGetOverlayUpperDir(t *testing.T) {
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
			name: "overlay with complex options",
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
			result := getOverlayUpperDir(tt.mounts)
			if result != tt.expected {
				t.Errorf("getOverlayUpperDir() = %q, want %q", result, tt.expected)
			}
		})
	}
}

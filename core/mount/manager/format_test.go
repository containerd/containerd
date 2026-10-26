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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestFormatMount(t *testing.T) {
	activeMounts := make([]mount.ActiveMount, 5)
	for i := range activeMounts {
		activeMounts[i] = mount.ActiveMount{
			Mount: mount.Mount{
				Source: fmt.Sprintf("/tmp/source/%d", i),
				Target: fmt.Sprintf("%d", i),
			},
			MountPoint: fmt.Sprintf("/tmp/mp/%d", i),
		}
	}

	for _, tc := range []struct {
		name      string
		mount     mount.Mount
		formatted mount.Mount
	}{
		{
			name: "empty mount",
		},
		{
			name: "no formatting",
			mount: mount.Mount{
				Type:   "bind",
				Source: "/var/lib/containerd",
				Target: "tmp",
			},
			formatted: mount.Mount{
				Type:   "bind",
				Source: "/var/lib/containerd",
				Target: "tmp",
			},
		},
		{
			name: "simple",
			mount: mount.Mount{
				Type:   "bind",
				Source: "{{ mount 0 }}",
				Target: "{{ target 0 }}",
			},
			formatted: mount.Mount{
				Type:   "bind",
				Source: "/tmp/mp/0",
				Target: "0",
			},
		},
		{
			name: "overlay",
			mount: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir={{ overlay 0 4 }}",
				},
			},
			formatted: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir=/tmp/mp/0:/tmp/mp/1:/tmp/mp/2:/tmp/mp/3:/tmp/mp/4",
				},
			},
		},
		{
			name: "overlay reversed",
			mount: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir={{ overlay 4 1 }}",
					"upperdir=/tmp/upper",
				},
			},
			formatted: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir=/tmp/mp/4:/tmp/mp/3:/tmp/mp/2:/tmp/mp/1",
					"upperdir=/tmp/upper",
				},
			},
		},
		{
			name: "overlay single",
			mount: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir={{ overlay 0 0 }}",
				},
			},
			formatted: mount.Mount{
				Type:   "overlay",
				Source: "overlay",
				Options: []string{
					"lowerdir=/tmp/mp/0",
				},
			},
		},
		{
			name: "copy",
			mount: mount.Mount{
				Type:   "bind",
				Source: "{{ source 0 }}",
				Target: "{{ target 0 }}",
			},
			formatted: mount.Mount{
				Type:   "bind",
				Source: "/tmp/source/0",
				Target: "0",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := mountFormatter{}.Transform(t.Context(), tc.mount, activeMounts)
			if err != nil {
				t.Fatalf("formatted failed: %v", err)
			}

			assert.Equal(t, tc.formatted, result, "Formatted mismatch")
		})
	}
}

func TestFormatString(t *testing.T) {
	activeMounts := make([]mount.ActiveMount, 3)
	for i := range activeMounts {
		activeMounts[i] = mount.ActiveMount{
			Mount: mount.Mount{
				Source: fmt.Sprintf("/source/%d", i),
				Target: fmt.Sprintf("/target/%d", i),
			},
			MountPoint: fmt.Sprintf("/mount/%d", i),
		}
	}

	tests := []struct {
		name    string
		format  string
		want    string
		wantErr bool
	}{
		{
			name:   "source",
			format: "{{ source 0 }}",
			want:   "/source/0",
		},
		{
			name:   "target",
			format: "{{ target 1 }}",
			want:   "/target/1",
		},
		{
			name:   "mount",
			format: "{{ mount 2 }}",
			want:   "/mount/2",
		},
		{
			name:   "overlay ascending",
			format: "{{ overlay 0 2 }}",
			want:   "/mount/0:/mount/1:/mount/2",
		},
		{
			name:   "overlay descending",
			format: "{{ overlay 2 0 }}",
			want:   "/mount/2:/mount/1:/mount/0",
		},
		{
			name:   "overlay single",
			format: "{{ overlay 1 1 }}",
			want:   "/mount/1",
		},
		{
			name:   "multiple actions",
			format: "{{ source 0 }}:{{ mount 1 }}",
			want:   "/source/0:/mount/1",
		},
		{
			name:   "literal text and suffix",
			format: "lowerdir={{ mount 0 }}/diff",
			want:   "lowerdir=/mount/0/diff",
		},
		{
			name:   "whitespace trimming",
			format: "before {{- mount 0 -}} after",
			want:   "before/mount/0after",
		},
		{
			name:    "negative index",
			format:  "{{ mount -1 }}",
			wantErr: true,
		},
		{
			name:    "out of bounds",
			format:  "{{ mount 3 }}",
			wantErr: true,
		},
		{
			name:    "invalid overlay range",
			format:  "{{ overlay 0 3 }}",
			wantErr: true,
		},
		{
			name:    "string argument",
			format:  `{{ mount "0" }}`,
			wantErr: true,
		},
		{
			name:    "unknown function",
			format:  "{{ unknown 0 }}",
			wantErr: true,
		},
		{
			name:    "invalid syntax",
			format:  "{{ mount 0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := formatString(tt.format)
			if format == nil {
				t.Fatal("expected formatter")
			}

			got, err := format(activeMounts)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			if assert.NoError(t, err) {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

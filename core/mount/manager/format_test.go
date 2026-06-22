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

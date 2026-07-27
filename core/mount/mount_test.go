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

package mount

import (
	"reflect"
	"testing"

	// required for `-test.root` flag not to fail
	_ "github.com/containerd/continuity/testutil"
)

func TestReadonlyMounts(t *testing.T) {
	testCases := []struct {
		desc     string
		input    []Mount
		expected []Mount
	}{
		{
			desc:     "empty slice",
			input:    []Mount{},
			expected: []Mount{},
		},
		{
			desc: "removes `upperdir` and `workdir` from overlay mounts, appends upper layer to lower",
			input: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
					},
				},
			},
			expected: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"lowerdir=/path/to/snapshots/4/fs:/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
					},
				},
			},
		},
		{
			desc: "removes `rw` and appends `ro` (once) to other mount types",
			input: []Mount{
				{
					Type:   "mount-without-rw",
					Source: "",
					Options: []string{
						"index=off",
						"workdir=/path/to/other/snapshots/work",
						"upperdir=/path/to/other/snapshots/2",
						"lowerdir=/path/to/other/snapshots/1",
					},
				},
				{
					Type:   "mount-with-rw",
					Source: "",
					Options: []string{
						"an-option=a-value",
						"another_opt=/another/value",
						"rw",
					},
				},
				{
					Type:   "mount-with-ro",
					Source: "",
					Options: []string{
						"an-option=a-value",
						"another_opt=/another/value",
						"ro",
					},
				},
			},
			expected: []Mount{
				{
					Type:   "mount-without-rw",
					Source: "",
					Options: []string{
						"index=off",
						"workdir=/path/to/other/snapshots/work",
						"upperdir=/path/to/other/snapshots/2",
						"lowerdir=/path/to/other/snapshots/1",
						"ro",
					},
				},
				{
					Type:   "mount-with-rw",
					Source: "",
					Options: []string{
						"an-option=a-value",
						"another_opt=/another/value",
						"ro",
					},
				},
				{
					Type:   "mount-with-ro",
					Source: "",
					Options: []string{
						"an-option=a-value",
						"another_opt=/another/value",
						"ro",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		if !reflect.DeepEqual(readonlyMounts(tc.input), tc.expected) {
			t.Fatalf("incorrectly modified mounts: %s", tc.desc)
		}
	}
}

func TestMountReadOnly(t *testing.T) {
	testCases := []struct {
		desc     string
		mount    Mount
		expected bool
	}{
		{
			desc:     "erofs is always read-only",
			mount:    Mount{Type: "erofs", Source: "/path/to/layer.erofs", Options: []string{"loop"}},
			expected: true,
		},
		{
			desc:     "overlay without upperdir is read-only",
			mount:    Mount{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower"}},
			expected: true,
		},
		{
			desc:     "overlay with upperdir is writable",
			mount:    Mount{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/upper"}},
			expected: false,
		},
		{
			desc:     "overlay with upperdir packed into a comma-joined options string is writable",
			mount:    Mount{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower,upperdir=/upper,workdir=/work"}},
			expected: false,
		},
		{
			desc:     "type modifiers are stripped before matching overlay",
			mount:    Mount{Type: "format/mkdir/overlay", Source: "overlay", Options: []string{"lowerdir=/lower"}},
			expected: true,
		},
		{
			desc:     "type modifiers are stripped, overlay with upperdir still writable",
			mount:    Mount{Type: "format/mkdir/overlay", Source: "overlay", Options: []string{"upperdir=/upper"}},
			expected: false,
		},
		{
			desc:     "overlay with an explicit `ro` option is read-only despite upperdir",
			mount:    Mount{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/upper", "ro"}},
			expected: true,
		},
		{
			desc:     "overlay `ro` packed into a comma-joined options string is read-only",
			mount:    Mount{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower,upperdir=/upper,ro"}},
			expected: true,
		},
		{
			desc:     "other types are read-only only with the `ro` option",
			mount:    Mount{Type: "bind", Source: "/path", Options: []string{"ro", "rbind"}},
			expected: true,
		},
		{
			desc:     "other types are writable without the `ro` option",
			mount:    Mount{Type: "bind", Source: "/path", Options: []string{"rbind"}},
			expected: false,
		},
	}

	for _, tc := range testCases {
		if got := tc.mount.ReadOnly(); got != tc.expected {
			t.Errorf("%s: ReadOnly() = %v, want %v", tc.desc, got, tc.expected)
		}
	}
}

func TestRemoveVolatileTempMount(t *testing.T) {
	testCases := []struct {
		desc     string
		input    []Mount
		expected []Mount
	}{
		{
			desc: "remove volatile option from overlay mounts, ignore non overlay",
			input: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
						"volatile",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"volatile",
					},
				},
			},
			expected: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"volatile",
					},
				},
			},
		},
		{
			desc: "remove fsync=volatile option from overlay mounts, ignore non overlay",
			input: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
						"fsync=volatile",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"fsync=volatile",
					},
				},
			},
			expected: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"fsync=volatile",
					},
				},
			},
		},
		{
			desc: "return original slice since no volatile options on overlay",
			input: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"volatile",
					},
				},
			},
			expected: []Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"index=off",
						"workdir=/path/to/snapshots/4/work",
						"upperdir=/path/to/snapshots/4/fs",
						"lowerdir=/path/to/snapshots/1/fs",
					},
				},
				{
					Type:   "underlay",
					Source: "underlay",
					Options: []string{
						"index=on",
						"lowerdir=/another/path/to/snapshots/2/fs",
						"volatile",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		original := copyMounts(tc.input)
		actual := RemoveVolatileOption(tc.input)
		if !reflect.DeepEqual(actual, tc.expected) {
			t.Fatalf("incorrectly modified mounts: %s.\n\n Expected: %v\n\n, Actual: %v", tc.desc, tc.expected, actual)
		}
		if !reflect.DeepEqual(original, tc.input) {
			t.Fatalf("modified original mounts: %s.\n\n Expected: %v\n\n, Actual: %v", tc.desc, original, tc.input)
		}
	}
}

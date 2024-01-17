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

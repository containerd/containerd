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

package docker

import (
	"testing"

	"github.com/containerd/containerd/reference"
	"gotest.tools/assert"
)

func TestOriginMatch(t *testing.T) {
	cases := []struct {
		name      string
		targetRef string
		origins   []string
		expected  []string
	}{
		{
			name:      "nil-origin",
			targetRef: "docker.io/some/repo@sha256:1b19eb80fc8e5c84ff912d75c228d968b30a6b7553ea44de94acc1958e320268",
			origins:   nil,
			expected:  nil,
		},
		{
			name:      "no-match",
			targetRef: "docker.io/some/repo@sha256:1b19eb80fc8e5c84ff912d75c228d968b30a6b7553ea44de94acc1958e320268",
			origins:   []string{"some-registry/some/repo"},
			expected:  nil,
		},
		{
			name:      "some-matches",
			targetRef: "docker.io/some/repo@sha256:1b19eb80fc8e5c84ff912d75c228d968b30a6b7553ea44de94acc1958e320268",
			origins:   []string{"some-registry/some/repo", "docker.io/other/repo", "docker.io/some/otherrepo"},
			expected:  []string{"docker.io/other/repo", "docker.io/some/otherrepo"},
		},
		{
			name:      "different-ports",
			targetRef: "some-registry:5000/some/repo@sha256:1b19eb80fc8e5c84ff912d75c228d968b30a6b7553ea44de94acc1958e320268",
			origins:   []string{"some-registry:5001/some/differentport"},
			expected:  nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			targetRef, err := reference.Parse(c.targetRef)
			assert.NilError(t, err)
			var origins []reference.Spec
			for _, o := range c.origins {
				ref, err := reference.Parse(o)
				assert.NilError(t, err)
				origins = append(origins, ref)
			}
			actualSpecs := findMatchingOrigins(targetRef, origins)
			var actual []string
			for _, spec := range actualSpecs {
				actual = append(actual, spec.String())
			}
			assert.DeepEqual(t, c.expected, actual)
		})
	}
}

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
	"reflect"
	"testing"

	digest "github.com/opencontainers/go-digest"
)

func TestGetManifestPath(t *testing.T) {
	for _, tc := range []struct {
		object   string
		dgst     digest.Digest
		expected []string
	}{
		{
			object:   "foo",
			dgst:     "bar",
			expected: []string{"manifests", "foo"},
		},
		{
			object:   "foo@bar",
			dgst:     "bar",
			expected: []string{"manifests", "foo"},
		},
		{
			object:   "foo@bar",
			dgst:     "foobar",
			expected: []string{"manifests", "foobar"},
		},
	} {
		if got := getManifestPath(tc.object, tc.dgst); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}

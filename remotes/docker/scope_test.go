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
	"context"
	"testing"

	"github.com/containerd/containerd/reference"
	"gotest.tools/assert"
)

func TestRepositoryScope(t *testing.T) {
	testCases := []struct {
		refspec  reference.Spec
		push     bool
		expected string
	}{
		{
			refspec: reference.Spec{
				Locator: "host/foo/bar",
				Object:  "ignored",
			},
			push:     false,
			expected: "repository:foo/bar:pull",
		},
		{
			refspec: reference.Spec{
				Locator: "host:4242/foo/bar",
				Object:  "ignored",
			},
			push:     true,
			expected: "repository:foo/bar:pull,push",
		},
	}
	for _, x := range testCases {
		t.Run(x.refspec.String(), func(t *testing.T) {
			actual, err := repositoryScope(x.refspec, x.push)
			assert.NilError(t, err)
			assert.Equal(t, x.expected, actual)
		})
	}
}

func TestGetTokenScopes(t *testing.T) {
	cases := []struct {
		name       string
		scopesFlat string
		expected   []string
	}{
		{
			name:       "Deduplication",
			scopesFlat: "repository:foo/bar:pull repository:foo/bar:push repository:foo/bar:pull,push repository:foo/bar:push,pull repository:bar/foo:pull",
			expected:   []string{"repository:bar/foo:pull", "repository:foo/bar:pull,push"},
		},
		{
			name:       "Merging",
			scopesFlat: "repository:foo/bar:pull repository:foo/bar:push",
			expected:   []string{"repository:foo/bar:pull,push"},
		},
		{
			name:       "Normalizing-Sort",
			scopesFlat: "repository:foo/bar:push,pull repository:bar/foo:pull",
			expected:   []string{"repository:bar/foo:pull", "repository:foo/bar:pull,push"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scopes, err := getTokenScopes(context.Background(), map[string]string{
				"scope": c.scopesFlat,
			})
			assert.NilError(t, err)
			assert.DeepEqual(t, scopes, c.expected)
		})
	}
}

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
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"

	"github.com/containerd/containerd/reference"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
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

type testAuthorizerCapturer struct {
	contexts []context.Context
}

func (a *testAuthorizerCapturer) Authorize(ctx context.Context, _ *http.Request) error {
	a.contexts = append(a.contexts, ctx)
	return nil
}

func (a *testAuthorizerCapturer) AddResponses(context.Context, []*http.Response) error {
	return nil
}

func TestMountShouldReturnErrAlreadyExist(t *testing.T) {
	authorizer := &testAuthorizerCapturer{}
	refspec, _ := reference.Parse("docker.io/test/nginx:latest")
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			rw.WriteHeader(404)
		}
		if r.Method == http.MethodPost {
			url := r.URL
			assert.Equal(t, "/blobs/uploads/", url.Path)
			assert.Equal(t, "sha256:0d093c962a6c2dd8bb8727b661e2b5f13e9df884af9945b4cc7088d9350cd3ee", r.FormValue("mount"))
			assert.Equal(t, "library/nginx", r.FormValue("from"))
			rw.WriteHeader(http.StatusCreated)
		}
	}))
	base, _ := url.Parse(s.URL)
	defer s.Close()
	p := dockerPusher{
		dockerBase: &dockerBase{
			client:  s.Client(),
			refspec: refspec,
			base:    *base,
			auth:    authorizer,
		},
		originProvider: func(_ ocispec.Descriptor) []reference.Spec {
			notmatch, _ := reference.Parse("otherregistry.io/test/nginx")
			match, _ := reference.Parse("docker.io/library/nginx")
			return []reference.Spec{notmatch, match}
		},
		tracker: NewInMemoryTracker(),
	}
	_, err := p.Push(context.Background(), ocispec.Descriptor{Digest: "sha256:0d093c962a6c2dd8bb8727b661e2b5f13e9df884af9945b4cc7088d9350cd3ee"})
	assert.Check(t, errors.Cause(err) == errdefs.ErrAlreadyExists)

	scopesForHead := tokenScopes{}.withEntry("repository:test/nginx", "push", "pull")
	scopesForMount := tokenScopes{}.withEntry("repository:test/nginx", "push", "pull").withEntry("repository:library/nginx", "pull")
	assert.Check(t, cmp.Len(authorizer.contexts, 2))
	actualScopesForHead := getContextScopes(authorizer.contexts[0])
	actualScopesForMount := getContextScopes(authorizer.contexts[1])
	assert.Check(t, actualScopesForHead.contains(scopesForHead))
	assert.Check(t, !actualScopesForHead.contains(scopesForMount))
	assert.Check(t, actualScopesForMount.contains(scopesForHead))
	assert.Check(t, actualScopesForMount.contains(scopesForMount))
}

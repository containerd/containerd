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

	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/stretchr/testify/assert"
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
			actual, err := RepositoryScope(x.refspec, x.push)
			assert.NoError(t, err)
			assert.Equal(t, x.expected, actual)
		})
	}
}

func TestRepositoryScopeForHost(t *testing.T) {
	testCases := []struct {
		name     string
		host     RegistryHost
		refspec  reference.Spec
		push     bool
		expected string
	}{
		{
			name: "no RepositoryPrefix yields the original repository",
			host: RegistryHost{Host: "registry-1.docker.io"},
			refspec: reference.Spec{
				Locator: "registry-1.docker.io/library/alpine",
				Object:  "ignored",
			},
			expected: "repository:library/alpine:pull",
		},
		{
			// Regression for https://github.com/containerd/containerd/issues/9648.
			name: "RepositoryPrefix is prepended to the repository name",
			host: RegistryHost{Host: "ghcr.io", RepositoryPrefix: "stackhpc/docker.io"},
			refspec: reference.Spec{
				Locator: "docker.io/calico/node",
				Object:  "v3.27.0",
			},
			expected: "repository:stackhpc/docker.io/calico/node:pull",
		},
		{
			name: "RepositoryPrefix with leading and trailing slashes is normalized",
			host: RegistryHost{Host: "us-docker.pkg.dev", RepositoryPrefix: "/project/quay-io/"},
			refspec: reference.Spec{
				Locator: "quay.io/prometheus/node-exporter",
				Object:  "v1.7.0",
			},
			expected: "repository:project/quay-io/prometheus/node-exporter:pull",
		},
		{
			name: "push action produces pull,push scope",
			host: RegistryHost{Host: "registry-1.docker.io"},
			refspec: reference.Spec{
				Locator: "registry-1.docker.io/library/alpine",
				Object:  "ignored",
			},
			push:     true,
			expected: "repository:library/alpine:pull,push",
		},
		{
			name: "push action with RepositoryPrefix produces prefixed pull,push scope",
			host: RegistryHost{Host: "ghcr.io", RepositoryPrefix: "stackhpc/docker.io"},
			refspec: reference.Spec{
				Locator: "docker.io/library/alpine",
				Object:  "ignored",
			},
			push:     true,
			expected: "repository:stackhpc/docker.io/library/alpine:pull,push",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := repositoryScopeForHost(tc.host, tc.refspec, tc.push)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestGetTokenScopes(t *testing.T) {
	testCases := []struct {
		scopesInCtx  []string
		commonScopes []string
		expected     []string
	}{
		{
			scopesInCtx:  []string{},
			commonScopes: []string{},
			expected:     []string{},
		},
		{
			scopesInCtx:  []string{},
			commonScopes: []string{"repository:foo/bar:pull"},
			expected:     []string{"repository:foo/bar:pull"},
		},
		{
			scopesInCtx:  []string{"repository:foo/bar:pull,push"},
			commonScopes: []string{},
			expected:     []string{"repository:foo/bar:pull,push"},
		},
		{
			scopesInCtx:  []string{"repository:foo/bar:pull"},
			commonScopes: []string{"repository:foo/bar:pull"},
			expected:     []string{"repository:foo/bar:pull"},
		},
		{
			scopesInCtx:  []string{"repository:foo/bar:pull"},
			commonScopes: []string{"repository:foo/bar:pull,push"},
			expected:     []string{"repository:foo/bar:pull", "repository:foo/bar:pull,push"},
		},
		{
			scopesInCtx:  []string{"repository:foo/bar:pull"},
			commonScopes: []string{"repository:foo/bar:pull,push", "repository:foo/bar:pull"},
			expected:     []string{"repository:foo/bar:pull", "repository:foo/bar:pull,push"},
		},
	}
	for _, tc := range testCases {
		ctx := context.WithValue(context.TODO(), tokenScopesKey{}, tc.scopesInCtx)
		actual := GetTokenScopes(ctx, tc.commonScopes)
		assert.Equal(t, tc.expected, actual)
	}
}

func TestCustomScope(t *testing.T) {
	scope := "whatever:foo/bar:pull"
	ctx := WithScope(context.Background(), scope)
	ctx = ContextWithAppendPullRepositoryScope(ctx, "foo/bar")

	scopes := GetTokenScopes(ctx, []string{})
	assert.Equal(t, []string{"repository:foo/bar:pull", scope}, scopes)
}

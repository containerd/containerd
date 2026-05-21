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
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/containerd/containerd/v2/pkg/reference"
)

// repositoryFromRefspec returns the repository name portion of a reference
// (e.g. "foo/bar" for "host/foo/bar:baz").
func repositoryFromRefspec(refspec reference.Spec) (string, error) {
	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}

// RepositoryScope returns a repository scope string such as "repository:foo/bar:pull"
// for "host/foo/bar:baz".
// When push is true, both pull and push are added to the scope.
func RepositoryScope(refspec reference.Spec, push bool) (string, error) {
	repo, err := repositoryFromRefspec(refspec)
	if err != nil {
		return "", err
	}
	s := "repository:" + repo + ":pull"
	if push {
		s += ",push"
	}
	return s, nil
}

// repositoryScopeForHost returns the repository scope appropriate for a
// token request to a specific host. When host.RepositoryPrefix is non-empty (set
// by the config loader for mirrors that namespace upstream repositories
// under a path prefix), the prefix is prepended to the repository name in
// the scope. Otherwise the result equals RepositoryScope.
func repositoryScopeForHost(host RegistryHost, refspec reference.Spec, push bool) (string, error) {
	repo, err := repositoryFromRefspec(refspec)
	if err != nil {
		return "", err
	}
	if prefix := strings.Trim(host.RepositoryPrefix, "/"); prefix != "" {
		repo = prefix + "/" + repo
	}
	s := "repository:" + repo + ":pull"
	if push {
		s += ",push"
	}
	return s, nil
}

// tokenScopesKey is used for the key for context.WithValue().
// value: []string (e.g. {"registry:foo/bar:pull"})
type tokenScopesKey struct{}

// ContextWithRepositoryScope returns a context with tokenScopesKey{} and the repository scope value.
func ContextWithRepositoryScope(ctx context.Context, refspec reference.Spec, push bool) (context.Context, error) {
	s, err := RepositoryScope(refspec, push)
	if err != nil {
		return nil, err
	}
	return WithScope(ctx, s), nil
}

// contextWithRepositoryScopeForHost returns a context with tokenScopesKey{}
// and the repository scope value derived from the given host. See
// repositoryScopeForHost for details on how mirror path prefixes are handled.
func contextWithRepositoryScopeForHost(ctx context.Context, host RegistryHost, refspec reference.Spec, push bool) (context.Context, error) {
	s, err := repositoryScopeForHost(host, refspec, push)
	if err != nil {
		return ctx, err
	}
	return WithScope(ctx, s), nil
}

// WithScope appends a custom registry auth scope to the context.
func WithScope(ctx context.Context, scope string) context.Context {
	var scopes []string
	if v := ctx.Value(tokenScopesKey{}); v != nil {
		parent := v.([]string)
		scopes = make([]string, 0, len(parent)+1)
		scopes = append(scopes, parent...)
		scopes = append(scopes, scope)
	} else {
		scopes = []string{scope}
	}
	return context.WithValue(ctx, tokenScopesKey{}, scopes)
}

// ContextWithAppendPullRepositoryScope is used to append repository pull
// scope into existing scopes indexed by the tokenScopesKey{}.
func ContextWithAppendPullRepositoryScope(ctx context.Context, repo string) context.Context {
	return WithScope(ctx, fmt.Sprintf("repository:%s:pull", repo))
}

// GetTokenScopes returns deduplicated and sorted scopes from ctx.Value(tokenScopesKey{}) and common scopes.
func GetTokenScopes(ctx context.Context, common []string) []string {
	scopes := []string{}
	if x := ctx.Value(tokenScopesKey{}); x != nil {
		scopes = append(scopes, x.([]string)...)
	}

	scopes = append(scopes, common...)
	sort.Strings(scopes)

	if len(scopes) == 0 {
		return scopes
	}

	l := 0
	for idx := 1; idx < len(scopes); idx++ {
		// Note: this comparison is unaware of the scope grammar (https://distribution.github.io/distribution/spec/auth/scope/)
		// So, "repository:foo/bar:pull,push" != "repository:foo/bar:push,pull", although semantically they are equal.
		if scopes[l] == scopes[idx] {
			continue
		}

		l++
		scopes[l] = scopes[idx]
	}
	return scopes[:l+1]
}

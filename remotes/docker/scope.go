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

	"github.com/containerd/containerd/reference"
)

// repositoryScope returns a repository scope string such as "repository:foo/bar:pull"
// for "host/foo/bar:baz".
// When push is true, both pull and push are added to the scope.
func repositoryScope(refspec reference.Spec, push bool) (string, error) {
	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return "", err
	}
	s := "repository:" + strings.TrimPrefix(u.Path, "/") + ":pull"
	if push {
		s += ",push"
	}
	return s, nil
}

// tokenScopesKey is used for the key for context.WithValue().
// value: []string (e.g. {"registry:foo/bar:pull"})
type tokenScopesKey struct{}

// contextWithRepositoryScope returns a context with tokenScopesKey{} and the repository scope value.
func contextWithRepositoryScope(ctx context.Context, refspec reference.Spec, push bool) (context.Context, error) {
	s, err := repositoryScope(refspec, push)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, tokenScopesKey{}, []string{s}), nil
}

type tokenScope struct {
	resource string
	actions  map[string]interface{}
}

func (ts tokenScope) String() string {
	var actionSlice []string
	for k := range ts.actions {
		actionSlice = append(actionSlice, k)
	}
	sort.Strings(actionSlice)
	return fmt.Sprintf("%s:%s", ts.resource, strings.Join(actionSlice, ","))
}

func parseTokenScope(s string) (tokenScope, error) {
	lastSep := strings.LastIndex(s, ":")
	if lastSep == -1 {
		return tokenScope{}, fmt.Errorf("%q is not a valid token scope", s)
	}
	actions := make(map[string]interface{})
	for _, a := range strings.Split(s[lastSep+1:], ",") {
		actions[a] = nil
	}
	return tokenScope{
		resource: s[:lastSep],
		actions:  actions,
	}, nil
}

// mergeTokenScope add a scope to an existing scope collection, ensuring deduplication
func mergeTokenScope(existing map[string]tokenScope, newElem tokenScope) {
	match, ok := existing[newElem.resource]
	if !ok {
		existing[newElem.resource] = newElem
		return
	}
	for k := range newElem.actions {
		match.actions[k] = nil
	}
	existing[newElem.resource] = match
}

// getTokenScopes returns deduplicated and sorted scopes from ctx.Value(tokenScopesKey{}) and params["scope"].
func getTokenScopes(ctx context.Context, params map[string]string) ([]string, error) {
	var rawScopes []string
	if x := ctx.Value(tokenScopesKey{}); x != nil {
		rawScopes = x.([]string)
	}
	if paramScopesFlat, ok := params["scope"]; ok {
		paramScopes := strings.Split(paramScopesFlat, " ")
		rawScopes = append(rawScopes, paramScopes...)
	}
	tokenScopes := map[string]tokenScope{}
	for _, rawScope := range rawScopes {
		parsedScope, err := parseTokenScope(rawScope)
		if err != nil {
			return nil, err
		}
		mergeTokenScope(tokenScopes, parsedScope)
	}
	var scopes []string
	for _, s := range tokenScopes {
		scopes = append(scopes, s.String())
	}
	sort.Strings(scopes)
	return scopes, nil
}

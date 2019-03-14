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
	"time"

	"gotest.tools/assert"
)

func (c *tokenCache) withEntry(host string, scopes tokenScopes, expires time.Time, token string) *tokenCache {
	c.add(host, scopes, expires, token)
	return c
}

func (s tokenScopes) withEntry(resource string, actions ...string) tokenScopes {
	actionsMap := map[string]interface{}{}
	for _, a := range actions {
		actionsMap[a] = nil
	}
	s.add(tokenScope{
		resource: resource,
		actions:  actionsMap,
	})
	return s
}

func TestTokenCache(t *testing.T) {
	cases := []struct {
		name            string
		cacheState      *tokenCache
		requestedHost   string
		requestedScopes tokenScopes
		expected        string
	}{
		{
			name:          "empty",
			cacheState:    newTokenCache(),
			requestedHost: "test",
			expected:      "",
		},
		{
			name:            "exact-match",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action"), time.Now().Add(time.Hour), "token"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "token",
		},
		{
			name:            "contains-more-actions",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action", "action2"), time.Now().Add(time.Hour), "token"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "token",
		},
		{
			name:            "contains-more-resources",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action").withEntry("resource2", "action"), time.Now().Add(time.Hour), "token"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "token",
		},
		{
			name:            "host-not-match",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action"), time.Now().Add(time.Hour), "token"),
			requestedHost:   "host2",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "",
		},
		{
			name:            "expired",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action"), time.Now().Add(-time.Hour), "token"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "",
		},
		{
			name:            "scope-mismatch",
			cacheState:      newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action2"), time.Now().Add(time.Hour), "token"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "",
		},
		{
			name: "multiple-candidates",
			cacheState: newTokenCache().withEntry("host1", tokenScopes{}.withEntry("resource", "action"), time.Now().Add(time.Hour), "token").
				withEntry("host1", tokenScopes{}.withEntry("resource2", "action"), time.Now().Add(time.Hour), "token2"),
			requestedHost:   "host1",
			requestedScopes: tokenScopes{}.withEntry("resource", "action"),
			expected:        "token",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := c.cacheState.get(c.requestedHost, c.requestedScopes)
			assert.Equal(t, result, c.expected)
		})
	}
}

func TestRemoveExpiredTokens(t *testing.T) {
	tokens := []token{
		{token: "1", expires: time.Now().Add(time.Hour)},
		{token: "2", expires: time.Now().Add(-time.Hour)},
		{token: "3", expires: time.Now().Add(-time.Hour)},
		{token: "4", expires: time.Now().Add(time.Hour)},
	}

	ht := hostTokens{
		tokens: tokens,
	}

	ht.removeExpired()
	assert.Check(t, len(ht.tokens) == 2)
	assert.Equal(t, "1", ht.tokens[0].token)
	assert.Equal(t, "4", ht.tokens[1].token)
}

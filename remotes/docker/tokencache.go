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
	"sync"
	"time"
)

type token struct {
	scopes  tokenScopes
	expires time.Time
	token   string
}

func (t *token) expired() bool {
	return t.expires.Before(time.Now())
}

func (t *token) satisfies(scopes tokenScopes) bool {
	return t.scopes.contains(scopes)
}

type hostTokens struct {
	mut    sync.RWMutex
	tokens []token
}

func (ht *hostTokens) removeExpiredNoLock() {
	valid := 0
	for _, t := range ht.tokens {
		if !t.expired() {
			ht.tokens[valid] = t
			valid++
		}
	}
	ht.tokens = ht.tokens[:valid]
}

func (ht *hostTokens) removeExpired() {
	ht.mut.Lock()
	defer ht.mut.Unlock()
	ht.removeExpiredNoLock()
}

func (ht *hostTokens) getForScopes(scopes tokenScopes) string {
	ht.removeExpired()
	ht.mut.RLock()
	defer ht.mut.RUnlock()
	for _, t := range ht.tokens {
		if t.satisfies(scopes) {
			return t.token
		}
	}
	return ""
}

func (ht *hostTokens) add(scopes tokenScopes, expires time.Time, t string) {
	ht.mut.Lock()
	defer ht.mut.Unlock()
	ht.removeExpiredNoLock()
	// the new token is likely to be the next one to use. prepending it then
	ht.tokens = append([]token{{scopes: scopes, expires: expires, token: t}}, ht.tokens...)
}

func newTokenCache() *tokenCache {
	return &tokenCache{
		tokens: make(map[string]*hostTokens),
	}
}

type tokenCache struct {
	mut    sync.RWMutex
	tokens map[string]*hostTokens
}

func (c *tokenCache) get(host string, scopes tokenScopes) string {
	c.mut.RLock()
	defer c.mut.RUnlock()
	if ht, ok := c.tokens[host]; ok {
		return ht.getForScopes(scopes)
	}
	return ""
}

func (c *tokenCache) getOrCreateHostTokens(host string) *hostTokens {
	c.mut.Lock()
	defer c.mut.Unlock()
	if ht, ok := c.tokens[host]; ok {
		return ht
	}
	ht := &hostTokens{}
	c.tokens[host] = ht
	return ht
}

func (c *tokenCache) add(host string, scopes tokenScopes, expires time.Time, token string) {
	ht := c.getOrCreateHostTokens(host)
	ht.add(scopes, expires, token)
}

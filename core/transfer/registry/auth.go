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

package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
)

// hosts returns the resolver hosts for ref: config.ConfigureHosts with
// username/secret credentials from creds, and every host's authorizer
// wrapped so a credential with a Header is sent as-is.
//
// Credentials are looked up once per host and cached until they expire, or
// for the life of the returned function when they carry no expiry. The cache
// is owned by the returned function, so it is scoped to the resolver of one
// OCIRegistry and is never shared between registries or transfers.
func hosts(ctx context.Context, ref string, creds CredentialHelper, opts config.HostOptions) docker.RegistryHosts {
	if creds == nil {
		return config.ConfigureHosts(ctx, opts)
	}
	cache := &credCache{ref: ref, creds: creds, entries: map[string]Credentials{}}
	opts.Credentials = func(host string) (string, string, error) {
		c, err := cache.get(host)
		if err != nil {
			return "", "", err
		}
		if c.Header != "" {
			// Sent by headerAuthorizer; never exchanged for a token.
			return "", "", nil
		}
		return c.Username, c.Secret, nil
	}
	configured := config.ConfigureHosts(ctx, opts)

	return func(host string) ([]docker.RegistryHost, error) {
		rhosts, err := configured(host)
		if err != nil {
			return nil, err
		}
		for i := range rhosts {
			rhosts[i].Authorizer = &headerAuthorizer{
				next:   rhosts[i].Authorizer,
				scheme: rhosts[i].Scheme,
				host:   rhosts[i].Host,
				cache:  cache,
			}
		}
		return rhosts, nil
	}
}

// credCache caches the credentials returned by a CredentialHelper per host.
type credCache struct {
	ref   string
	creds CredentialHelper

	mu      sync.Mutex
	entries map[string]Credentials
}

// get returns the credentials for host, asking the helper when none are
// cached or the cached credentials have expired. Errors are not cached.
// The lock is held across the helper call so concurrent requests share one
// lookup.
func (c *credCache) get(host string) (Credentials, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if creds, ok := c.entries[host]; ok {
		if creds.Expires.IsZero() || time.Now().Before(creds.Expires) {
			return creds, nil
		}
		delete(c.entries, host)
	}
	creds, err := c.creds.GetCredentials(context.Background(), c.ref, host)
	if err != nil {
		return Credentials{}, err
	}
	c.entries[host] = creds
	return creds, nil
}

// headerAuthorizer sends a caller-supplied Authorization header to one
// host and otherwise defers to the next authorizer.
//
// Credentials are always looked up for the configured host, never for the
// host of the request: a redirect must not let the registry choose which
// of the caller's credentials are used.
type headerAuthorizer struct {
	next   docker.Authorizer // may be nil
	scheme string
	host   string
	cache  *credCache
}

// matches reports whether req is for the host the header is for. Redirect
// targets must also keep the scheme. Other requests are built by the
// resolver for this host, including the pusher's upload URL after the
// transport's https-to-http fallback.
func (a *headerAuthorizer) matches(req *http.Request) bool {
	if req.URL.Host != a.host {
		return false
	}
	return req.Response == nil || req.URL.Scheme == a.scheme
}

func (a *headerAuthorizer) Authorize(ctx context.Context, req *http.Request) error {
	c, err := a.cache.get(a.host)
	if err != nil {
		return err
	}
	if c.Header != "" {
		// Also called from CheckRedirect for redirect targets. net/http
		// copies Authorization to redirects on the same hostname with any
		// port or scheme and to subdomains, so remove it unless the
		// request matches.
		if a.matches(req) {
			req.Header.Set("Authorization", c.Header)
		} else {
			req.Header.Del("Authorization")
		}
		return nil
	}
	if a.next == nil {
		return nil
	}
	return a.next.Authorize(ctx, req)
}

func (a *headerAuthorizer) AddResponses(ctx context.Context, rs []*http.Response) error {
	c, err := a.cache.get(a.host)
	if err != nil {
		return err
	}
	if c.Header != "" {
		last := rs[len(rs)-1].Request
		if last == nil || !a.matches(last) {
			// A redirect target never received the header; its
			// challenge is not a rejection of the header.
			return errdefs.ErrNotImplemented
		}
		if last.Header.Get("Authorization") != c.Header {
			// The failed request did not carry the current header, for
			// example because it was refreshed in flight; try it.
			return nil
		}
		// The supplied header was refused; do not follow the challenge.
		return fmt.Errorf("registry rejected the supplied authorization header: %w", docker.ErrInvalidAuthorization)
	}
	if a.next == nil {
		return errdefs.ErrNotImplemented
	}
	return a.next.AddResponses(ctx, rs)
}

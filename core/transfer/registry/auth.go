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
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

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
	cache := newCredCache(ref, creds)
	opts.Credentials = func(host string) (string, string, error) {
		// The docker authorizer gives no context for this callback.
		c, err := cache.get(context.Background(), host)
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
	pending map[string]*credLookup
	sent    map[authKey]struct{}
}

func newCredCache(ref string, creds CredentialHelper) *credCache {
	return &credCache{
		ref:     ref,
		creds:   creds,
		entries: map[string]Credentials{},
		pending: map[string]*credLookup{},
		sent:    map[authKey]struct{}{},
	}
}

// origin is a normalized scheme, hostname and port.
type origin struct {
	scheme   string
	hostname string
	port     string
}

// newOrigin returns the origin of scheme and host (host[:port]), with the
// scheme and hostname lowercased and the default port for the scheme filled
// in.
func newOrigin(scheme, host string) origin {
	scheme = strings.ToLower(scheme)
	u := url.URL{Scheme: scheme, Host: host}
	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return origin{scheme: scheme, hostname: strings.ToLower(u.Hostname()), port: port}
}

// authKey identifies an Authorization value sent to an origin, so the value
// can be recognized later without keeping it.
type authKey [sha256.Size]byte

func newAuthKey(scheme, host, value string) authKey {
	o := newOrigin(scheme, host)
	h := sha256.New()
	var n [8]byte
	for _, f := range []string{o.scheme, o.hostname, o.port, value} {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write([]byte(f))
	}
	var k authKey
	h.Sum(k[:0])
	return k
}

// markSent records that the authorization value identified by k was sent.
func (c *credCache) markSent(k authKey) {
	c.mu.Lock()
	c.sent[k] = struct{}{}
	c.mu.Unlock()
}

// wasSent reports whether the authorization value identified by k was sent.
func (c *credCache) wasSent(k authKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.sent[k]
	return ok
}

func expired(creds Credentials) bool {
	return !creds.Expires.IsZero() && !time.Now().Before(creds.Expires)
}

// credLookup is a helper call in flight for one host.
type credLookup struct {
	ctx   context.Context
	done  chan struct{}
	creds Credentials
	err   error
}

// get returns the credentials for host, asking the helper when none are
// cached or the cached credentials have expired. Errors are not cached.
//
// Concurrent requests for a host share one helper call, made with the
// context of the request that started it. Each request waits only until
// its own context is done; the call itself runs to completion, so a
// helper with ordered responses, such as the auth stream, stays in step.
func (c *credCache) get(ctx context.Context, host string) (Credentials, error) {
	for {
		c.mu.Lock()
		if creds, ok := c.entries[host]; ok {
			if !expired(creds) {
				c.mu.Unlock()
				return creds, nil
			}
			delete(c.entries, host)
		}
		l, ok := c.pending[host]
		if !ok {
			l = &credLookup{ctx: ctx, done: make(chan struct{})}
			c.pending[host] = l
			go c.lookup(host, l)
		}
		c.mu.Unlock()

		select {
		case <-l.done:
		case <-ctx.Done():
			return Credentials{}, ctx.Err()
		}
		if l.err != nil && l.ctx.Err() != nil && ctx.Err() == nil {
			// Failed because the request that started it is gone; ask
			// again with this request's context.
			continue
		}
		return l.creds, l.err
	}
}

func (c *credCache) lookup(host string, l *credLookup) {
	creds, err := c.creds.GetCredentials(l.ctx, c.ref, host)
	c.mu.Lock()
	delete(c.pending, host)
	if err == nil && !expired(creds) {
		c.entries[host] = creds
	}
	c.mu.Unlock()
	l.creds, l.err = creds, err
	close(l.done)
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

// matches reports whether req is for the host the header is for, comparing
// normalized hostnames and ports. Requests built by the resolver for this
// host may use http after the transport's https-to-http fallback, which
// keeps the host and port; their default port is that of a.scheme.
// Redirect targets must keep the scheme, or use the scheme the transport
// used for the redirecting request to this host after a fallback.
func (a *headerAuthorizer) matches(req *http.Request) bool {
	if req.Response == nil {
		return a.sameHost(req.URL)
	}
	scheme := strings.ToLower(req.URL.Scheme)
	if scheme == strings.ToLower(a.scheme) {
		return a.sameHost(req.URL)
	}
	// net/http sets Response.Request to the request the transport sent,
	// which is the http request after a fallback. The fallback keeps the
	// host and port, so the origin it used is a.host under its scheme.
	prev := req.Response.Request
	if prev == nil {
		return false
	}
	used := newOrigin(scheme, a.host)
	return newOrigin(prev.URL.Scheme, prev.URL.Host) == used && newOrigin(scheme, req.URL.Host) == used
}

// sameHost reports whether u has the hostname and port of a.host.
func (a *headerAuthorizer) sameHost(u *url.URL) bool {
	got, want := newOrigin(a.scheme, u.Host), newOrigin(a.scheme, a.host)
	return got.hostname == want.hostname && got.port == want.port
}

func (a *headerAuthorizer) Authorize(ctx context.Context, req *http.Request) error {
	c, err := a.cache.get(ctx, a.host)
	if err != nil {
		// Credentials are looked up before any challenge, so a failing
		// helper must not break registries that need none. Send the
		// request without a header; a challenge reports the error from
		// AddResponses.
		log.G(ctx).WithError(err).WithField("host", a.host).Debug("failed to get credentials, continuing without")
		c = Credentials{}
	}
	if c.Header != "" {
		// Also called from CheckRedirect for redirect targets. net/http
		// copies Authorization to redirects on the same hostname with any
		// port or scheme and to subdomains, so remove it unless the
		// request matches.
		if a.matches(req) {
			req.Header.Set("Authorization", c.Header)
			a.cache.markSent(newAuthKey(a.scheme, a.host, c.Header))
		} else {
			req.Header.Del("Authorization")
		}
		return nil
	}
	if req.Response != nil {
		// A redirect may carry a header this authorizer sent before the
		// credentials were refreshed or failed. Remove only that value;
		// other Authorization values are left to the next authorizer.
		if v := req.Header.Get("Authorization"); v != "" && a.cache.wasSent(newAuthKey(a.scheme, a.host, v)) {
			req.Header.Del("Authorization")
		}
	}
	if a.next == nil {
		return nil
	}
	return a.next.Authorize(ctx, req)
}

func (a *headerAuthorizer) AddResponses(ctx context.Context, rs []*http.Response) error {
	c, err := a.cache.get(ctx, a.host)
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

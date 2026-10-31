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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/streaming"
)

var (
	testManifest       = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	testManifestDigest = digest.FromBytes(testManifest)
	testBlob           = []byte("blob content")
	testBlobDigest     = digest.FromBytes(testBlob)
)

// fakeRegistry serves a manifest at /v2/test/manifests/latest and a blob at
// /v2/test/blobs/<testBlobDigest> to requests carrying wantAuth. Other
// requests get a 401 with the challenge, if set.
type fakeRegistry struct {
	*httptest.Server
	wantAuth  string
	challenge string
	blobURL   string // if set, blob requests are redirected here

	mu   sync.Mutex
	auth []string // Authorization header of every request
}

func newFakeRegistry(t *testing.T, wantAuth, challenge string) *fakeRegistry {
	r := &fakeRegistry{wantAuth: wantAuth, challenge: challenge}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.Close)
	return r
}

func (r *fakeRegistry) host() string {
	u, _ := url.Parse(r.URL)
	return u.Host
}

func (r *fakeRegistry) authHeaders() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.auth...)
}

func (r *fakeRegistry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.auth = append(r.auth, req.Header.Get("Authorization"))
	r.mu.Unlock()

	if req.Header.Get("Authorization") != r.wantAuth {
		if r.challenge != "" {
			w.Header().Set("WWW-Authenticate", r.challenge)
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body []byte
	switch req.URL.Path {
	case "/v2/test/manifests/latest":
		body = testManifest
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
		w.Header().Set("Docker-Content-Digest", testManifestDigest.String())
	case "/v2/test/blobs/uploads/":
		// Absolute http Location, as a registry behind the localhost
		// https-to-http fallback returns.
		w.Header().Set("Location", "http://"+req.Host+"/upload/1")
		w.WriteHeader(http.StatusAccepted)
		return
	case "/upload/1":
		dgst := digest.Digest(req.URL.Query().Get("digest"))
		b, _ := io.ReadAll(req.Body)
		if digest.FromBytes(b) != dgst {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Docker-Content-Digest", dgst.String())
		w.WriteHeader(http.StatusCreated)
		return
	case "/v2/test/blobs/" + testBlobDigest.String():
		if r.blobURL != "" {
			http.Redirect(w, req, r.blobURL, http.StatusTemporaryRedirect)
			return
		}
		body = testBlob
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if req.Method != http.MethodHead {
		w.Write(body)
	}
}

// tokenServer counts requests and issues token for any request.
func newTokenServer(t *testing.T, token string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q,"access_token":%q}`, token, token)
	}))
	t.Cleanup(s.Close)
	return s, &n
}

type fakeCreds struct {
	creds map[string]Credentials
	err   error
	calls sync.Map // host -> *atomic.Int32
}

func (f *fakeCreds) GetCredentials(ctx context.Context, ref, host string) (Credentials, error) {
	n, _ := f.calls.LoadOrStore(host, new(atomic.Int32))
	n.(*atomic.Int32).Add(1)
	if f.err != nil {
		return Credentials{}, f.err
	}
	return f.creds[host], nil
}

func (f *fakeCreds) callCount(host string) int32 {
	n, ok := f.calls.Load(host)
	if !ok {
		return 0
	}
	return n.(*atomic.Int32).Load()
}

func resolve(t *testing.T, ref string, creds CredentialHelper) error {
	t.Helper()
	r, err := NewOCIRegistry(context.Background(), ref, WithCredentials(creds), WithDefaultScheme("http"))
	if err != nil {
		t.Fatalf("NewOCIRegistry(%q) failed: %v", ref, err)
	}
	_, desc, err := r.Resolve(context.Background())
	if err == nil && desc.Digest != testManifestDigest {
		t.Errorf("Resolve(%q) digest = %s, want %s", ref, desc.Digest, testManifestDigest)
	}
	return err
}

func TestHeaderSentWithoutChallenge(t *testing.T) {
	ts, tokenRequests := newTokenServer(t, "unused")
	reg := newFakeRegistry(t, "Bearer good", fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL))
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}

	if err := resolve(t, reg.host()+"/test:latest", creds); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	for i, a := range reg.authHeaders() {
		if a != "Bearer good" {
			t.Errorf("request %d Authorization = %q, want %q", i, a, "Bearer good")
		}
	}
	if n := tokenRequests.Load(); n != 0 {
		t.Errorf("token server got %d requests, want 0", n)
	}
}

func TestRejectedHeaderDoesNotFallBack(t *testing.T) {
	ts, tokenRequests := newTokenServer(t, "fallback")
	reg := newFakeRegistry(t, "Bearer fallback", fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL))
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer bad", Username: "user", Secret: "pass"},
	}}

	err := resolve(t, reg.host()+"/test:latest", creds)
	if !errors.Is(err, docker.ErrInvalidAuthorization) {
		t.Fatalf("Resolve error = %v, want %v", err, docker.ErrInvalidAuthorization)
	}
	if n := tokenRequests.Load(); n != 0 {
		t.Errorf("token server got %d requests, want 0", n)
	}
	for i, a := range reg.authHeaders() {
		if a != "Bearer bad" {
			t.Errorf("request %d Authorization = %q, want %q", i, a, "Bearer bad")
		}
	}
}

func TestHeaderOnlyForItsHost(t *testing.T) {
	regA := newFakeRegistry(t, "Bearer a", "")
	regB := newFakeRegistry(t, "Basic dXNlcjpwYXNz", `Basic realm="test"`) // user:pass
	creds := &fakeCreds{creds: map[string]Credentials{
		regA.host(): {Header: "Bearer a"},
		regB.host(): {Username: "user", Secret: "pass"},
	}}

	if err := resolve(t, regB.host()+"/test:latest", creds); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	got := regB.authHeaders()
	if len(got) != 2 || got[0] != "" || got[1] != "Basic dXNlcjpwYXNz" {
		t.Errorf("host B Authorization headers = %q, want [\"\" \"Basic dXNlcjpwYXNz\"]", got)
	}
	if n := len(regA.authHeaders()); n != 0 {
		t.Errorf("host A got %d requests, want 0", n)
	}
}

func TestUsernameSecretUnchanged(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		reg := newFakeRegistry(t, "Basic dXNlcjpwYXNz", `Basic realm="test"`)
		creds := &fakeCreds{creds: map[string]Credentials{
			reg.host(): {Username: "user", Secret: "pass"},
		}}
		if err := resolve(t, reg.host()+"/test:latest", creds); err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
	})
	t.Run("token", func(t *testing.T) {
		ts, tokenRequests := newTokenServer(t, "issued")
		reg := newFakeRegistry(t, "Bearer issued", fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL))
		creds := &fakeCreds{creds: map[string]Credentials{
			reg.host(): {Username: "user", Secret: "pass"},
		}}
		if err := resolve(t, reg.host()+"/test:latest", creds); err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if n := tokenRequests.Load(); n == 0 {
			t.Error("token server got no requests")
		}
	})
	t.Run("anonymous", func(t *testing.T) {
		reg := newFakeRegistry(t, "", "")
		if err := resolve(t, reg.host()+"/test:latest", &fakeCreds{}); err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
	})
}

func TestRedirectDoesNotLeakHeader(t *testing.T) {
	// net/http drops Authorization on redirects to another hostname but
	// keeps it for the same hostname on another port; cover both.
	for _, cdnHost := range []string{"127.0.0.1", "localhost"} {
		t.Run(cdnHost, func(t *testing.T) { testRedirectDoesNotLeakHeader(t, cdnHost) })
	}
}

func testRedirectDoesNotLeakHeader(t *testing.T, cdnHost string) {
	var cdnAuth atomic.Value
	cdnAuth.Store("")
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a := req.Header.Get("Authorization"); a != "" {
			cdnAuth.Store(a)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(testBlob)))
		w.Write(testBlob)
	}))
	t.Cleanup(cdn.Close)

	reg := newFakeRegistry(t, "Bearer good", "")
	u, _ := url.Parse(cdn.URL)
	reg.blobURL = "http://" + cdnHost + ":" + u.Port() + "/blob"
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}

	ctx := context.Background()
	ref := reg.host() + "/test:latest"
	r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.Fetcher(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := f.Fetch(ctx, ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    testBlobDigest,
		Size:      int64(len(testBlob)),
	})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	b, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(b) != string(testBlob) {
		t.Fatalf("Fetch read = %q, %v; want %q", b, err, testBlob)
	}
	if a := cdnAuth.Load().(string); a != "" {
		t.Errorf("redirect target got Authorization %q, want none", a)
	}
}

func TestCredentialsCache(t *testing.T) {
	ts, _ := newTokenServer(t, "issued")
	tokenChallenge := fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL)

	for _, tc := range []struct {
		name      string
		wantAuth  string
		challenge string
		creds     Credentials
		wantCalls int32
	}{
		{
			name:      "header without expiry",
			wantAuth:  "Bearer good",
			creds:     Credentials{Header: "Bearer good"},
			wantCalls: 1,
		},
		{
			name:      "header not yet expired",
			wantAuth:  "Bearer good",
			creds:     Credentials{Header: "Bearer good", Expires: time.Now().Add(time.Hour)},
			wantCalls: 1,
		},
		{
			// Every request re-asks the helper.
			name:      "header expired",
			wantAuth:  "Bearer good",
			creds:     Credentials{Header: "Bearer good", Expires: time.Now().Add(-time.Second)},
			wantCalls: 3,
		},
		{
			// The challenge is answered once per resolve, but the
			// helper is asked once.
			name:      "username and secret",
			wantAuth:  "Bearer issued",
			challenge: tokenChallenge,
			creds:     Credentials{Username: "user", Secret: "pass"},
			wantCalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := newFakeRegistry(t, tc.wantAuth, tc.challenge)
			creds := &fakeCreds{creds: map[string]Credentials{reg.host(): tc.creds}}
			ctx := context.Background()
			ref := reg.host() + "/test:latest"
			r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				if _, _, err := r.Resolve(ctx); err != nil {
					t.Fatalf("Resolve #%d failed: %v", i, err)
				}
			}
			if n := creds.callCount(reg.host()); n != tc.wantCalls {
				t.Errorf("GetCredentials called %d times for %s, want %d", n, reg.host(), tc.wantCalls)
			}
		})
	}
}

func TestCredentialsCacheNotShared(t *testing.T) {
	reg := newFakeRegistry(t, "Bearer good", "")
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}
	ctx := context.Background()
	ref := reg.host() + "/test:latest"
	for i := 0; i < 2; i++ {
		r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.Resolve(ctx); err != nil {
			t.Fatalf("Resolve #%d failed: %v", i, err)
		}
	}
	if n := creds.callCount(reg.host()); n != 2 {
		t.Errorf("GetCredentials called %d times for 2 registries, want 2", n)
	}
}

func TestCredentialsErrorNotCached(t *testing.T) {
	reg := newFakeRegistry(t, "Bearer good", "")
	errHelper := errors.New("helper failed")
	creds := &fakeCreds{
		creds: map[string]Credentials{reg.host(): {Header: "Bearer good"}},
		err:   errHelper,
	}
	ctx := context.Background()
	ref := reg.host() + "/test:latest"
	r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Resolve(ctx); !errors.Is(err, errHelper) {
		t.Fatalf("Resolve error = %v, want %v", err, errHelper)
	}
	creds.err = nil
	if _, _, err := r.Resolve(ctx); err != nil {
		t.Fatalf("Resolve after helper recovered failed: %v", err)
	}
}

// pipeStream is one end of an in-memory bidirectional stream.
type pipeStream struct {
	send chan<- typeurl.Any
	recv <-chan typeurl.Any
	done chan struct{}
	once *sync.Once
}

func (p *pipeStream) Send(a typeurl.Any) error {
	select {
	case p.send <- a:
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *pipeStream) Recv() (typeurl.Any, error) {
	select {
	case a := <-p.recv:
		return a, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *pipeStream) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

type memStreams struct {
	mu      sync.Mutex
	streams map[string]streaming.Stream
}

func (m *memStreams) Create(ctx context.Context, id string) (streaming.Stream, error) {
	a, b := make(chan typeurl.Any), make(chan typeurl.Any)
	done, once := make(chan struct{}), new(sync.Once)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streams == nil {
		m.streams = map[string]streaming.Stream{}
	}
	m.streams[id] = &pipeStream{send: b, recv: a, done: done, once: once}
	return &pipeStream{send: a, recv: b, done: done, once: once}, nil
}

func (m *memStreams) Get(ctx context.Context, id string) (streaming.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.streams[id]
	if !ok {
		return nil, fmt.Errorf("stream %q not found", id)
	}
	return s, nil
}

func TestProxiedHeader(t *testing.T) {
	ts, tokenRequests := newTokenServer(t, "unused")
	reg := newFakeRegistry(t, "Bearer good", fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL))
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ref := reg.host() + "/test:latest"
	client, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
	if err != nil {
		t.Fatal(err)
	}
	sm := &memStreams{}
	a, err := client.MarshalAny(ctx, sm)
	if err != nil {
		t.Fatalf("MarshalAny failed: %v", err)
	}
	var server OCIRegistry
	if err := server.UnmarshalAny(ctx, sm, a); err != nil {
		t.Fatalf("UnmarshalAny failed: %v", err)
	}
	_, desc, err := server.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if desc.Digest != testManifestDigest {
		t.Errorf("Resolve digest = %s, want %s", desc.Digest, testManifestDigest)
	}
	if n := tokenRequests.Load(); n != 0 {
		t.Errorf("token server got %d requests, want 0", n)
	}
}

func TestProxiedCredentials(t *testing.T) {
	expires := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for _, want := range []Credentials{
		{Host: "registry.example", Header: "Bearer good", Expires: expires},
		{Host: "registry.example", Username: "user", Secret: "pass", Expires: expires},
		{Host: "registry.example", Header: "Bearer good"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		client, err := NewOCIRegistry(ctx, "registry.example/test:latest",
			WithCredentials(&fakeCreds{creds: map[string]Credentials{want.Host: want}}))
		if err != nil {
			t.Fatal(err)
		}
		sm := &memStreams{}
		a, err := client.MarshalAny(ctx, sm)
		if err != nil {
			t.Fatalf("MarshalAny failed: %v", err)
		}
		var server OCIRegistry
		if err := server.UnmarshalAny(ctx, sm, a); err != nil {
			t.Fatalf("UnmarshalAny failed: %v", err)
		}
		got, err := server.creds.GetCredentials(ctx, server.reference, want.Host)
		cancel()
		if err != nil {
			t.Fatalf("GetCredentials failed: %v", err)
		}
		if !got.Expires.Equal(want.Expires) {
			t.Errorf("GetCredentials Expires = %v, want %v", got.Expires, want.Expires)
		}
		got.Expires, want.Expires = time.Time{}, time.Time{}
		if got != want {
			t.Errorf("GetCredentials = %+v, want %+v", got, want)
		}
	}
}

func TestHeaderAuthorizerAuthorize(t *testing.T) {
	const host = "registry.example.com"
	for _, tc := range []struct {
		name   string
		header string // Header returned by the helper
		url    string
		before string // Authorization already on the request
		direct bool   // request built by the resolver, not a redirect
		want   string
	}{
		{name: "same scheme and host", header: "Bearer good", url: "https://registry.example.com/v2/", direct: true, want: "Bearer good"},
		{name: "replaces stale value", header: "Bearer good", url: "https://registry.example.com/v2/", direct: true, before: "Bearer old", want: "Bearer good"},
		{name: "redirect to same scheme and host", header: "Bearer good", url: "https://registry.example.com/v2/blob", want: "Bearer good"},
		{name: "direct after http fallback", header: "Bearer good", url: "http://registry.example.com/upload", direct: true, want: "Bearer good"},
		{name: "direct to other host", header: "Bearer good", url: "https://cdn.example.net/blob", direct: true, before: "Bearer good"},
		{name: "scheme downgrade", header: "Bearer good", url: "http://registry.example.com/v2/", before: "Bearer good"},
		{name: "other port", header: "Bearer good", url: "https://registry.example.com:8443/v2/", before: "Bearer good"},
		{name: "subdomain", header: "Bearer good", url: "https://cdn.registry.example.com/blob", before: "Bearer good"},
		{name: "refreshed header on redirect", header: "Bearer new", url: "https://cdn.example.net/blob", before: "Bearer old"},
		{name: "no header leaves request alone", url: "https://cdn.example.net/blob", before: "Basic other", want: "Basic other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &headerAuthorizer{
				scheme: "https",
				host:   host,
				cache: &credCache{
					creds:   &fakeCreds{creds: map[string]Credentials{host: {Header: tc.header}}},
					entries: map[string]Credentials{},
				},
			}
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.before != "" {
				req.Header.Set("Authorization", tc.before)
			}
			if !tc.direct {
				req.Response = &http.Response{StatusCode: http.StatusTemporaryRedirect}
			}
			if err := a.Authorize(context.Background(), req); err != nil {
				t.Fatalf("Authorize(%s) failed: %v", tc.url, err)
			}
			if got := req.Header.Get("Authorization"); got != tc.want {
				t.Errorf("Authorize(%s) Authorization = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestRedirectTargetChallenge(t *testing.T) {
	var cdnAuth atomic.Value
	cdnAuth.Store("")
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a := req.Header.Get("Authorization"); a != "" {
			cdnAuth.Store(a)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="cdn"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(cdn.Close)

	reg := newFakeRegistry(t, "Bearer good", "")
	reg.blobURL = cdn.URL + "/blob"
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}

	ctx := context.Background()
	ref := reg.host() + "/test:latest"
	r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds), WithDefaultScheme("http"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.Fetcher(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := f.Fetch(ctx, ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    testBlobDigest,
		Size:      int64(len(testBlob)),
	})
	if err == nil {
		// The request is made on first read.
		_, err = io.ReadAll(rc)
		rc.Close()
	}
	if err == nil {
		t.Fatal("Fetch succeeded, want error from redirect target")
	}
	if errors.Is(err, docker.ErrInvalidAuthorization) {
		t.Errorf("Fetch error = %v, want an error not wrapping %v", err, docker.ErrInvalidAuthorization)
	}
	if a := cdnAuth.Load().(string); a != "" {
		t.Errorf("redirect target got Authorization %q, want none", a)
	}
}

func TestHeaderPushHTTPFallback(t *testing.T) {
	reg := newFakeRegistry(t, "Bearer good", "")
	creds := &fakeCreds{creds: map[string]Credentials{
		reg.host(): {Header: "Bearer good"},
	}}

	// No default scheme: a loopback host tries https first and falls back
	// to http in the transport.
	ctx := context.Background()
	ref := reg.host() + "/test:latest"
	r, err := NewOCIRegistry(ctx, ref, WithCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	blob := []byte("pushed blob")
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	p, err := r.Pusher(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	w, err := p.Push(ctx, desc)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if _, err := w.Write(blob); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := w.Commit(ctx, desc.Size, desc.Digest); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	for i, a := range reg.authHeaders() {
		if a != "Bearer good" {
			t.Errorf("request %d Authorization = %q, want %q", i, a, "Bearer good")
		}
	}
}

// seqCreds returns creds[i] on the i-th call and the last entry after that.
type seqCreds struct {
	mu    sync.Mutex
	creds []Credentials
	calls int
}

func (s *seqCreds) GetCredentials(ctx context.Context, ref, host string) (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.creds[min(s.calls, len(s.creds)-1)]
	s.calls++
	return c, nil
}

func TestHeaderChangedInFlight(t *testing.T) {
	expired := time.Now().Add(-time.Second)
	for _, tc := range []struct {
		name  string
		first Credentials
	}{
		{name: "refreshed header", first: Credentials{Header: "Bearer old", Expires: expired}},
		{name: "header after anonymous", first: Credentials{Expires: expired}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, tokenRequests := newTokenServer(t, "unused")
			reg := newFakeRegistry(t, "Bearer good", fmt.Sprintf(`Bearer realm=%q,service="test"`, ts.URL))
			creds := &seqCreds{creds: []Credentials{tc.first, {Header: "Bearer good"}}}

			if err := resolve(t, reg.host()+"/test:latest", creds); err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			got := reg.authHeaders()
			if len(got) != 2 || got[0] != tc.first.Header || got[1] != "Bearer good" {
				t.Errorf("Authorization headers = %q, want [%q %q]", got, tc.first.Header, "Bearer good")
			}
			if n := tokenRequests.Load(); n != 0 {
				t.Errorf("token server got %d requests, want 0", n)
			}
		})
	}
}

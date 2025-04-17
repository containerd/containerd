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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker/auth"
	remoteerrors "github.com/containerd/containerd/v2/core/remotes/errors"
)

func TestHTTPResolver(t *testing.T) {
	s := func(h http.Handler) (string, ResolverOptions, func()) {
		s := httptest.NewServer(h)

		options := ResolverOptions{}
		base := s.URL[7:] // strip "http://"
		return base, options, s.Close
	}
	runBasicTest(t, "testname", s)
}

func TestHTTPSResolver(t *testing.T) {
	runBasicTest(t, "testname", tlsServer)
}

func TestResolverOptionsRace(t *testing.T) {
	header := http.Header{}
	header.Set("X-Test", "test")

	s := func(h http.Handler) (string, ResolverOptions, func()) {
		s := httptest.NewServer(h)

		options := ResolverOptions{
			Headers: header,
		}
		base := s.URL[7:] // strip "http://"
		return base, options, s.Close
	}

	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("test ResolverOptions race %d", i), func(t *testing.T) {
			// parallel sub tests so the race condition (if not handled) can be caught
			// by race detector
			t.Parallel()
			runBasicTest(t, "testname", s)
		})
	}
}

func TestBasicResolver(t *testing.T) {
	basicAuth := func(h http.Handler) (string, ResolverOptions, func()) {
		// Wrap with basic auth
		wrapped := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()
			if !ok || username != "user1" || password != "password1" {
				rw.Header().Set("WWW-Authenticate", "Basic realm=localhost")
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(rw, r)
		})

		base, options, close := tlsServer(wrapped)
		authorizer := NewDockerAuthorizer(
			WithAuthClient(options.Client),
			WithAuthCreds(func(host string) (string, string, error) {
				return "user1", "password1", nil
			}),
		)
		options.Hosts = ConfigureDefaultRegistries(
			WithClient(options.Client),
			WithAuthorizer(authorizer),
		)
		return base, options, close
	}
	runBasicTest(t, "testname", basicAuth)
}

func TestAnonymousTokenResolver(t *testing.T) {
	th := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(`{"access_token":"perfectlyvalidopaquetoken"}`))
	})

	runBasicTest(t, "testname", withTokenServer(th, nil))
}

func TestBasicAuthTokenResolver(t *testing.T) {
	th := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		username, password, ok := r.BasicAuth()
		if !ok || username != "user1" || password != "password1" {
			rw.Write([]byte(`{"access_token":"insufficientscope"}`))
		} else {
			rw.Write([]byte(`{"access_token":"perfectlyvalidopaquetoken"}`))
		}
	})
	creds := func(string) (string, string, error) {
		return "user1", "password1", nil
	}

	runBasicTest(t, "testname", withTokenServer(th, creds))
}

func TestRefreshTokenResolver(t *testing.T) {
	th := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)

		r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "somerefreshtoken" {
			rw.Write([]byte(`{"access_token":"insufficientscope"}`))
		} else {
			rw.Write([]byte(`{"access_token":"perfectlyvalidopaquetoken"}`))
		}
	})
	creds := func(string) (string, string, error) {
		return "", "somerefreshtoken", nil
	}

	runBasicTest(t, "testname", withTokenServer(th, creds))
}

func TestFetchRefreshToken(t *testing.T) {
	f := func(t *testing.T, disablePOST bool) {
		name := "testname"
		if disablePOST {
			name += "-disable-post"
		}
		var fetchedRefreshToken string
		onFetchRefreshToken := func(ctx context.Context, refreshToken string, req *http.Request) {
			fetchedRefreshToken = refreshToken
		}
		srv := newRefreshTokenServer(t, name, disablePOST, onFetchRefreshToken)
		runBasicTest(t, name, srv.BasicTestFunc())
		if fetchedRefreshToken != srv.RefreshToken {
			t.Errorf("unexpected refresh token: got %q", fetchedRefreshToken)
		}
	}

	t.Run("POST", func(t *testing.T) {
		f(t, false)
	})
	t.Run("GET", func(t *testing.T) {
		f(t, true)
	})
}

func TestPostBasicAuthTokenResolver(t *testing.T) {
	th := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)

		r.ParseForm()
		if r.PostForm.Get("grant_type") != "password" || r.PostForm.Get("username") != "user1" || r.PostForm.Get("password") != "password1" {
			rw.Write([]byte(`{"access_token":"insufficientscope"}`))
		} else {
			rw.Write([]byte(`{"access_token":"perfectlyvalidopaquetoken"}`))
		}
	})
	creds := func(string) (string, string, error) {
		return "user1", "password1", nil
	}

	runBasicTest(t, "testname", withTokenServer(th, creds))
}

func TestBasicAuthResolver(t *testing.T) {
	creds := func(string) (string, string, error) {
		return "totallyvaliduser", "totallyvalidpassword", nil
	}

	runBasicTest(t, "testname", withBasicAuthServer(creds))
}

func TestBadTokenResolver(t *testing.T) {
	th := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(`{"access_token":"insufficientscope"}`))
	})
	creds := func(string) (string, string, error) {
		return "", "somerefreshtoken", nil
	}

	ctx := context.Background()
	h := newContent(ocispec.MediaTypeImageManifest, []byte("not anything parse-able"))

	base, ro, close := withTokenServer(th, creds)(logHandler{t, h})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/doesntmatter:sometag", base)

	_, _, err := resolver.Resolve(ctx, image)
	if err == nil {
		t.Fatal("Expected error getting token with inssufficient scope")
	}
	if !errors.Is(err, ErrInvalidAuthorization) {
		t.Fatal(err)
	}
}

func TestMissingBasicAuthResolver(t *testing.T) {
	creds := func(string) (string, string, error) {
		return "", "", nil
	}

	ctx := context.Background()
	h := newContent(ocispec.MediaTypeImageManifest, []byte("not anything parse-able"))

	base, ro, close := withBasicAuthServer(creds)(logHandler{t, h})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/doesntmatter:sometag", base)

	_, _, err := resolver.Resolve(ctx, image)
	if err == nil {
		t.Fatal("Expected error getting token with inssufficient scope")
	}
	if !errors.Is(err, ErrInvalidAuthorization) {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), "no basic auth credentials") {
		t.Fatalf("expected \"no basic auth credentials\" message, got %s", err.Error())
	}
}

func TestWrongBasicAuthResolver(t *testing.T) {
	creds := func(string) (string, string, error) {
		return "totallyvaliduser", "definitelythewrongpassword", nil
	}

	ctx := context.Background()
	h := newContent(ocispec.MediaTypeImageManifest, []byte("not anything parse-able"))

	base, ro, close := withBasicAuthServer(creds)(logHandler{t, h})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/doesntmatter:sometag", base)

	_, _, err := resolver.Resolve(ctx, image)
	if err == nil {
		t.Fatal("Expected error getting token with inssufficient scope")
	}
	var rerr remoteerrors.ErrUnexpectedStatus
	if !errors.As(err, &rerr) {
		t.Fatal(err)
	}
	if rerr.StatusCode != 403 {
		t.Fatalf("expected 403 status code, got %d", rerr.StatusCode)
	}
}

func TestHostFailureFallbackResolver(t *testing.T) {
	sf := func(h http.Handler) (string, ResolverOptions, func()) {
		s := httptest.NewServer(h)
		base := s.URL[7:] // strip "http://"

		options := ResolverOptions{}
		createHost := func(host string) RegistryHost {
			return RegistryHost{
				Client: &http.Client{
					// Set the timeout so we timeout waiting for the non-responsive HTTP server
					Timeout: 500 * time.Millisecond,
				},
				Host:         host,
				Scheme:       "http",
				Path:         "/v2",
				Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityPush,
			}
		}

		// Create an unstarted HTTP server. We use this to generate a random port.
		notRunning := httptest.NewUnstartedServer(nil)
		notRunningBase := notRunning.Listener.Addr().String()

		// Override hosts with two hosts
		options.Hosts = func(host string) ([]RegistryHost, error) {
			return []RegistryHost{
				createHost(notRunningBase), // This host IS running, but with a non-responsive HTTP server
				createHost(base),           // This host IS running
			}, nil
		}

		return base, options, s.Close
	}

	runBasicTest(t, "testname", sf)
}

func TestHostTLSFailureFallbackResolver(t *testing.T) {
	sf := func(h http.Handler) (string, ResolverOptions, func()) {
		// Start up two servers
		server := httptest.NewServer(h)
		httpBase := server.URL[7:] // strip "http://"

		tlsServer := httptest.NewUnstartedServer(h)
		tlsServer.StartTLS()
		httpsBase := tlsServer.URL[8:] // strip "https://"

		capool := x509.NewCertPool()
		cert, _ := x509.ParseCertificate(tlsServer.TLS.Certificates[0].Certificate[0])
		capool.AddCert(cert)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: capool,
				},
			},
		}

		options := ResolverOptions{}
		createHost := func(host string) RegistryHost {
			return RegistryHost{
				Client:       client,
				Host:         host,
				Scheme:       "https",
				Path:         "/v2",
				Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityPush,
			}
		}

		// Override hosts with two hosts
		options.Hosts = func(host string) ([]RegistryHost, error) {
			return []RegistryHost{
				createHost(httpBase),  // This host is serving plain HTTP
				createHost(httpsBase), // This host is serving TLS
			}, nil
		}

		return httpBase, options, func() {
			server.Close()
			tlsServer.Close()
		}
	}

	runBasicTest(t, "testname", sf)
	runNotFoundTest(t, "testname", sf)
}

func TestHTTPFallbackResolver(t *testing.T) {
	sf := func(h http.Handler) (string, ResolverOptions, func()) {
		s := httptest.NewServer(h)
		u, err := url.Parse(s.URL)
		if err != nil {
			t.Fatal(err)
		}

		client := &http.Client{
			Transport: NewHTTPFallback(http.DefaultTransport),
		}
		options := ResolverOptions{
			Hosts: func(host string) ([]RegistryHost, error) {
				return []RegistryHost{
					{
						Client:       client,
						Host:         u.Host,
						Scheme:       "https",
						Path:         "/v2",
						Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityPush,
					},
				}, nil
			},
		}
		return u.Host, options, s.Close
	}

	runBasicTest(t, "testname", sf)
}

func TestHTTPFallbackTimeoutResolver(t *testing.T) {
	sf := func(h http.Handler) (string, ResolverOptions, func()) {

		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{
			Handler:           h,
			ReadHeaderTimeout: time.Second,
		}
		go func() {
			// Accept first connection but do not do anything with it
			// to force TLS handshake to timeout. Subsequent connection
			// will be HTTP and should work.
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(time.Second)
				c.Close()
			}()
			server.Serve(l)
		}()
		host := l.Addr().String()

		defaultTransport := &http.Transport{
			TLSHandshakeTimeout: time.Millisecond,
		}
		client := &http.Client{
			Transport: NewHTTPFallback(defaultTransport),
		}

		options := ResolverOptions{
			Hosts: func(host string) ([]RegistryHost, error) {
				return []RegistryHost{
					{
						Client:       client,
						Host:         host,
						Scheme:       "https",
						Path:         "/v2",
						Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityPush,
					},
				}, nil
			},
		}
		return host, options, func() { l.Close() }
	}

	runBasicTest(t, "testname", sf)
}

func TestHTTPFallbackPortError(t *testing.T) {
	// This test only checks the isPortError since testing the whole http fallback would
	// require listening on 80 and making sure nothing is listening on 443.

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := l.Addr().String()
	err = l.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = net.Dial("tcp", host)
	if err == nil {
		t.Fatal("Dial should fail after close")
	}

	if isPortError(err, host) {
		t.Fatalf("Expected no port error for %s with %v", host, err)
	}
	if !isPortError(err, "127.0.0.1") {
		t.Fatalf("Expected port error for 127.0.0.1 with %v", err)
	}

}

func TestResolveProxy(t *testing.T) {
	var (
		ctx  = context.Background()
		tag  = "latest"
		r    = http.NewServeMux()
		name = "testname"
		ns   = "upstream.example.com"
	)

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("1")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("2")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())
	m.RegisterHandler(r, name)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, tag), mc)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)

	nr := namespaceRouter{
		"upstream.example.com": r,
	}

	base, ro, close := tlsServer(logHandler{t, nr})
	defer close()

	ro.Hosts = func(host string) ([]RegistryHost, error) {
		return []RegistryHost{{
			Client:       ro.Client,
			Host:         base,
			Scheme:       "https",
			Path:         "/v2",
			Capabilities: HostCapabilityPull | HostCapabilityResolve,
		}}, nil
	}

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/%s:%s", ns, name, tag)

	_, d, err := resolver.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	f, err := resolver.Fetcher(ctx, image)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := testocimanifest(ctx, f, d)
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 2 {
		t.Fatalf("Unexpected number of references: %d, expected 2", len(refs))
	}

	for _, ref := range refs {
		if err := testFetch(ctx, f, ref); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveProxyFallback(t *testing.T) {
	var (
		ctx  = context.Background()
		tag  = "latest"
		r    = http.NewServeMux()
		name = "testname"
	)

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("1")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("2")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())
	m.RegisterHandler(r, name)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, tag), mc)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)

	nr := namespaceRouter{
		"": r,
	}
	s := httptest.NewServer(logHandler{t, nr})
	defer s.Close()

	base := s.URL[7:] // strip "http://"

	ro := ResolverOptions{
		Hosts: func(host string) ([]RegistryHost, error) {
			return []RegistryHost{
				{
					Host:         flipLocalhost(host),
					Scheme:       "http",
					Path:         "/v2",
					Capabilities: HostCapabilityPull | HostCapabilityResolve,
				},
				{
					Host:         host,
					Scheme:       "http",
					Path:         "/v2",
					Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityPush,
				},
			}, nil
		},
	}

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/%s:%s", base, name, tag)

	_, d, err := resolver.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	f, err := resolver.Fetcher(ctx, image)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := testocimanifest(ctx, f, d)
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 2 {
		t.Fatalf("Unexpected number of references: %d, expected 2", len(refs))
	}

	for _, ref := range refs {
		if err := testFetch(ctx, f, ref); err != nil {
			t.Fatal(err)
		}
	}
}

func flipLocalhost(host string) string {
	if strings.HasPrefix(host, "127.0.0.1") {
		return "localhost" + host[9:]

	} else if strings.HasPrefix(host, "localhost") {
		return "127.0.0.1" + host[9:]
	}
	return host
}

func withTokenServer(th http.Handler, creds func(string) (string, string, error)) func(h http.Handler) (string, ResolverOptions, func()) {
	return func(h http.Handler) (string, ResolverOptions, func()) {
		s := httptest.NewUnstartedServer(th)
		s.StartTLS()

		cert, _ := x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])
		tokenBase := s.URL + "/token"

		// Wrap with token auth
		wrapped := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			auth := strings.ToLower(r.Header.Get("Authorization"))
			if auth != "bearer perfectlyvalidopaquetoken" {
				authHeader := fmt.Sprintf("Bearer realm=%q,service=registry,scope=\"repository:testname:pull,pull\"", tokenBase)
				if strings.HasPrefix(auth, "bearer ") {
					authHeader = authHeader + ",error=" + auth[7:]
				}
				rw.Header().Set("WWW-Authenticate", authHeader)
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(rw, r)
		})

		base, options, close := tlsServer(wrapped)
		options.Hosts = ConfigureDefaultRegistries(
			WithClient(options.Client),
			WithAuthorizer(NewDockerAuthorizer(
				WithAuthClient(options.Client),
				WithAuthCreds(creds),
			)),
		)
		options.Client.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(cert)
		return base, options, func() {
			s.Close()
			close()
		}
	}
}

func withBasicAuthServer(creds func(string) (string, string, error)) func(h http.Handler) (string, ResolverOptions, func()) {
	return func(h http.Handler) (string, ResolverOptions, func()) {
		// Wrap with basic auth
		wrapped := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			user, password, ok := r.BasicAuth()
			if ok {
				if user != "totallyvaliduser" || password != "totallyvalidpassword" {
					rw.WriteHeader(http.StatusForbidden)
					rw.Write([]byte(`{"errors":[{"code":"DENIED"}]}`))
					return
				}
			} else {
				authHeader := "Basic realm=\"testserver\""
				rw.Header().Set("WWW-Authenticate", authHeader)
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(rw, r)
		})

		base, options, close := tlsServer(wrapped)
		options.Hosts = ConfigureDefaultRegistries(
			WithClient(options.Client),
			WithAuthorizer(NewDockerAuthorizer(
				WithAuthCreds(creds),
			)),
		)
		return base, options, close
	}
}

func tlsServer(h http.Handler) (string, ResolverOptions, func()) {
	s := httptest.NewUnstartedServer(h)
	s.StartTLS()

	capool := x509.NewCertPool()
	cert, _ := x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])
	capool.AddCert(cert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: capool,
			},
		},
	}

	options := ResolverOptions{
		Hosts: ConfigureDefaultRegistries(WithClient(client)),
		// Set deprecated field for tests to use for configuration
		Client: client,
	}
	base := s.URL[8:] // strip "https://"
	return base, options, s.Close
}

type logHandler struct {
	t       *testing.T
	handler http.Handler
}

func (h logHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(rw, r)
}

type namespaceRouter map[string]http.Handler

func (nr namespaceRouter) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	h, ok := nr[r.URL.Query().Get("ns")]
	if !ok {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	h.ServeHTTP(rw, r)
}

func runBasicTest(t *testing.T, name string, sf func(h http.Handler) (string, ResolverOptions, func())) {
	var (
		ctx = context.Background()
		tag = "latest"
		r   = http.NewServeMux()
	)

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("1")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("2")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())
	m.RegisterHandler(r, name)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, tag), mc)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)

	base, ro, close := sf(logHandler{t, r})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/%s:%s", base, name, tag)

	_, d, err := resolver.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	f, err := resolver.Fetcher(ctx, image)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := testocimanifest(ctx, f, d)
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 2 {
		t.Fatalf("Unexpected number of references: %d, expected 2", len(refs))
	}

	for _, ref := range refs {
		if err := testFetch(ctx, f, ref); err != nil {
			t.Fatal(err)
		}
	}
}

func runNotFoundTest(t *testing.T, name string, sf func(h http.Handler) (string, ResolverOptions, func())) {
	var (
		ctx = context.Background()
		tag = "latest"
		r   = http.NewServeMux()
	)

	base, ro, close := sf(logHandler{t, r})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/%s:%s", base, name, tag)

	_, _, err := resolver.Resolve(ctx, image)
	if err == nil {
		t.Fatalf("Expected error resolving %s, got nil", image)
	}
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Fatalf("Expected error resolving %s to be ErrNotFound, got %v", image, err)
	}
}

func testFetch(ctx context.Context, f remotes.Fetcher, desc ocispec.Descriptor) error {
	r, err := f.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	dgstr := desc.Digest.Algorithm().Digester()
	io.Copy(dgstr.Hash(), r)
	if dgstr.Digest() != desc.Digest {
		return fmt.Errorf("content mismatch: %s != %s", dgstr.Digest(), desc.Digest)
	}

	fByDigest, ok := f.(remotes.FetcherByDigest)
	if !ok {
		return fmt.Errorf("fetcher %T does not implement FetcherByDigest", f)
	}
	r2, desc2, err := fByDigest.FetchByDigest(ctx, desc.Digest)
	if err != nil {
		return fmt.Errorf("FetcherByDigest: faild to fetch %v: %w", desc.Digest, err)
	}
	if desc2.Size != desc.Size {
		r2b, err := io.ReadAll(r2)
		if err != nil {
			return fmt.Errorf("FetcherByDigest: size mismatch: %d != %d (content: %v)", desc2.Size, desc.Size, err)
		}
		return fmt.Errorf("FetcherByDigest: size mismatch: %d != %d (content: %q)", desc2.Size, desc.Size, string(r2b))
	}
	dgstr2 := desc.Digest.Algorithm().Digester()
	if _, err = io.Copy(dgstr2.Hash(), r2); err != nil {
		return fmt.Errorf("FetcherByDigest: faild to copy: %w", err)
	}
	if dgstr2.Digest() != desc.Digest {
		return fmt.Errorf("FetcherByDigest: content mismatch: %s != %s", dgstr2.Digest(), desc.Digest)
	}
	return nil
}

func testocimanifest(ctx context.Context, f remotes.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	r, err := f.Fetch(ctx, desc)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", desc.Digest, err)
	}
	p, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if dgst := desc.Digest.Algorithm().FromBytes(p); dgst != desc.Digest {
		return nil, fmt.Errorf("digest mismatch: %s != %s", dgst, desc.Digest)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, err
	}

	var descs []ocispec.Descriptor

	descs = append(descs, manifest.Config)
	descs = append(descs, manifest.Layers...)

	return descs, nil
}

type testContent struct {
	mediaType string
	content   []byte
}

func newContent(mediaType string, b []byte) testContent {
	return testContent{
		mediaType: mediaType,
		content:   b,
	}
}

func (tc testContent) Descriptor() ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: tc.mediaType,
		Digest:    digest.FromBytes(tc.content),
		Size:      int64(len(tc.content)),
	}
}

func (tc testContent) Digest() digest.Digest {
	return digest.FromBytes(tc.content)
}

func (tc testContent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", tc.mediaType)
	w.Header().Add("Content-Length", strconv.Itoa(len(tc.content)))
	w.Header().Add("Docker-Content-Digest", tc.Digest().String())
	w.WriteHeader(http.StatusOK)
	w.Write(tc.content)
}

type testManifest struct {
	config     testContent
	references []testContent
}

func newManifest(config testContent, refs ...testContent) testManifest {
	return testManifest{
		config:     config,
		references: refs,
	}
}

func (m testManifest) OCIManifest() []byte {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 1,
		},
		Config: m.config.Descriptor(),
		Layers: make([]ocispec.Descriptor, len(m.references)),
	}
	for i, c := range m.references {
		manifest.Layers[i] = c.Descriptor()
	}
	b, _ := json.Marshal(manifest)
	return b
}

func (m testManifest) RegisterHandler(r *http.ServeMux, name string) {
	for _, c := range append(m.references, m.config) {
		r.Handle(fmt.Sprintf("/v2/%s/blobs/%s", name, c.Digest()), c)
	}
}

func newRefreshTokenServer(t testing.TB, name string, disablePOST bool, onFetchRefreshToken OnFetchRefreshToken) *refreshTokenServer {
	return &refreshTokenServer{
		T:                   t,
		Name:                name,
		DisablePOST:         disablePOST,
		OnFetchRefreshToken: onFetchRefreshToken,
		AccessToken:         "testAccessToken-" + name,
		RefreshToken:        "testRefreshToken-" + name,
		Username:            "testUser-" + name,
		Password:            "testPassword-" + name,
	}
}

type refreshTokenServer struct {
	T                   testing.TB
	Name                string
	DisablePOST         bool
	OnFetchRefreshToken OnFetchRefreshToken
	AccessToken         string
	RefreshToken        string
	Username            string
	Password            string
}

func (srv *refreshTokenServer) isValidAuthorizationHeader(s string) bool {
	fields := strings.Fields(s)
	return len(fields) == 2 && strings.ToLower(fields[0]) == "bearer" && (fields[1] == srv.RefreshToken || fields[1] == srv.AccessToken)
}

func (srv *refreshTokenServer) BasicTestFunc() func(h http.Handler) (string, ResolverOptions, func()) {
	t := srv.T
	return func(h http.Handler) (string, ResolverOptions, func()) {
		wrapped := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/token" {
				if !srv.isValidAuthorizationHeader(r.Header.Get("Authorization")) {
					realm := fmt.Sprintf("https://%s/token", r.Host)
					wwwAuthenticateHeader := fmt.Sprintf("Bearer realm=%q,service=registry,scope=\"repository:%s:pull\"", realm, srv.Name)
					rw.Header().Set("WWW-Authenticate", wwwAuthenticateHeader)
					rw.WriteHeader(http.StatusUnauthorized)
					return
				}
				h.ServeHTTP(rw, r)
				return
			}
			switch r.Method {
			case http.MethodGet: // https://distribution.github.io/distribution/spec/auth/token/#requesting-a-token
				u, p, ok := r.BasicAuth()
				if !ok || u != srv.Username || p != srv.Password {
					rw.WriteHeader(http.StatusForbidden)
					return
				}
				var resp auth.FetchTokenResponse
				resp.Token = srv.AccessToken
				resp.AccessToken = srv.AccessToken // alias of Token
				query := r.URL.Query()
				switch query.Get("offline_token") {
				case "true":
					resp.RefreshToken = srv.RefreshToken
				case "false", "":
				default:
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				b, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					return
				}
				rw.WriteHeader(http.StatusOK)
				rw.Header().Set("Content-Type", "application/json")
				t.Logf("GET mode: returning JSON %q, for query %+v", string(b), query)
				rw.Write(b)
			case http.MethodPost: // https://distribution.github.io/distribution/spec/auth/oauth/#getting-a-token
				if srv.DisablePOST {
					rw.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				r.ParseForm()
				pf := r.PostForm
				if pf.Get("grant_type") != "password" {
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				if pf.Get("username") != srv.Username || pf.Get("password") != srv.Password {
					rw.WriteHeader(http.StatusForbidden)
					return
				}
				var resp auth.OAuthTokenResponse
				resp.AccessToken = srv.AccessToken
				switch pf.Get("access_type") {
				case "offline":
					resp.RefreshToken = srv.RefreshToken
				case "online", "":
				default:
					rw.WriteHeader(http.StatusBadRequest)
					return
				}
				b, err := json.Marshal(resp)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					return
				}
				rw.WriteHeader(http.StatusOK)
				rw.Header().Set("Content-Type", "application/json")
				t.Logf("POST mode: returning JSON %q, for form %+v", string(b), pf)
				rw.Write(b)
			default:
				rw.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		})

		base, options, close := tlsServer(wrapped)
		authorizer := NewDockerAuthorizer(
			WithAuthClient(options.Client),
			WithAuthCreds(func(string) (string, string, error) {
				return srv.Username, srv.Password, nil
			}),
			WithFetchRefreshToken(srv.OnFetchRefreshToken),
		)
		options.Hosts = ConfigureDefaultRegistries(
			WithClient(options.Client),
			WithAuthorizer(authorizer),
		)
		return base, options, close
	}
}

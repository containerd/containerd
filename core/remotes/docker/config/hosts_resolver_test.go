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

package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/remotes/docker"
)

func TestResolverWithHostsDir(t *testing.T) {
	tests := []struct {
		name         string
		enableMirror bool
		enableServer bool
	}{
		{"enable both mirror and server", true, true},
		{"enable mirror only", true, false},
		{"enable server only", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testResolverWithHostsDir(t, "testname", tt.enableMirror, tt.enableServer)
		})
	}
}

// testResolverWithHostsDir tests the resolver with a hosts.toml file in the host directory.
// The hosts.toml has a server and mirror.
// It bootstraps 4 httptest servers: upstream, mirror, server, and token.
// It can test the following scenarios:
// 1. Both mirror and server are enabled -> requests are handled by mirror
// 2. Only mirror is enabled -> requests are handled by mirror
// 3. Only server is enabled -> requests are handled by server
//
// The test ensures the following:
// 1. No requests are sent to upstream server (since `server` is specified)
// 2. Requests are handled (only) by expected http server
// 3. Requests (only) contain the expected header overrides from the hosts.toml
// e.g., mirror header overrides are not applied to server requests, and vice versa
// 4. Header overrides are applied to both registry and token server.
func testResolverWithHostsDir(t *testing.T, name string, enableMirror, enableServer bool) {
	const hostTOMLTemplate = `
server = "%s"
[header]
  User-Agent = "server"
  x-server-custom-1 = "custom server header"

[host."%s".header]
  User-Agent = "mirror"
  x-mirror-custom-1 = "custom mirror header"
`

	mirrorOnlyHeader := func(t *testing.T, r *http.Request) {
		assert.Equal(t, "mirror", r.Header.Get("User-Agent"))
		assert.Equal(t, "custom mirror header", r.Header.Get("x-mirror-custom-1"))
		assert.Empty(t, r.Header.Get("x-server-custom-1")) // no header override from server
	}

	serverOnlyHeader := func(t *testing.T, r *http.Request) {
		assert.Equal(t, "server", r.Header.Get("User-Agent"))
		assert.Empty(t, r.Header.Get("x-mirror-custom-1")) // no header override from mirror
		assert.Equal(t, "custom server header", r.Header.Get("x-server-custom-1"))
	}

	dir := t.TempDir()
	var mirrorTriggered, serverTriggered atomic.Bool

	testFn := func(h http.Handler) (string, docker.ResolverOptions, func()) {
		tokenHandler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			// ensure token request has the correct headers
			switch {
			case enableMirror:
				mirrorOnlyHeader(t, r)
			case enableServer:
				serverOnlyHeader(t, r)
			}
			if r.Method != http.MethodGet {
				rw.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusOK)
			rw.Write([]byte(`{"access_token":"perfectlyvalidopaquetoken"}`))
		})
		tokenServer, tokenCert := newTLSServer(tokenHandler)
		tokenBase := tokenServer.URL + "/token"

		// Wrap with token auth
		tokenWrapped := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
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

		upstream, upstreamCert := newTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			t.Fatal("upstream server shouldn't be called, given server is specified in hosts.toml")
		}))
		base := upstream.URL[8:] // strip "https://"
		mirror, mirrorCert := newTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			mirrorTriggered.Store(true)
			mirrorOnlyHeader(t, r)
			tokenWrapped.ServeHTTP(rw, r)
		}))
		server, serverCert := newTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			serverTriggered.Store(true)
			serverOnlyHeader(t, r)
		}))

		capool := x509.NewCertPool()
		for _, cert := range []*x509.Certificate{tokenCert, upstreamCert, mirrorCert, serverCert} {
			capool.AddCert(cert)
		}

		hostTOML := fmt.Sprintf(hostTOMLTemplate, server.URL, mirror.URL)
		var hostDir, hostFilePath string
		if runtime.GOOS == "windows" {
			hostDir = filepath.Join(dir, strings.ReplaceAll(hostDirectory(base), ":", ""))
		} else {
			hostDir = filepath.Join(dir, hostDirectory(base))
		}
		hostFilePath = filepath.Join(hostDir, "hosts.toml")

		err := os.MkdirAll(hostDir, 0755)
		assert.NoError(t, err)
		err = os.WriteFile(hostFilePath, []byte(hostTOML), 0644)
		assert.NoError(t, err)

		hostOptions := HostOptions{
			HostDir: HostDirFromRoot(dir),
			DefaultTLS: &tls.Config{
				RootCAs: capool,
			},
		}

		options := docker.ResolverOptions{
			Hosts: ConfigureHosts(context.TODO(), hostOptions),
		}

		if !enableMirror {
			mirror.Close()
		}
		if !enableServer {
			server.Close()
		}
		closer := func() {
			tokenServer.Close()
			upstream.Close()
			if enableMirror {
				mirror.Close()
			}
			if enableServer {
				server.Close()
			}
		}

		return base, options, closer
	}

	runBasicTest(t, name, testFn)

	switch {
	case enableMirror:
		assert.True(t, mirrorTriggered.Load())
		assert.False(t, serverTriggered.Load())
	case enableServer:
		assert.False(t, mirrorTriggered.Load())
		assert.True(t, serverTriggered.Load())
	default:
		assert.False(t, mirrorTriggered.Load())
		assert.False(t, serverTriggered.Load())
	}
}

func runBasicTest(t *testing.T, name string, sf func(h http.Handler) (string, docker.ResolverOptions, func())) {
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

	base, ro, close := sf(r)
	defer close()

	resolver := docker.NewResolver(ro)
	image := fmt.Sprintf("%s/%s:%s", base, name, tag)

	_, _, err := resolver.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
}

func newTLSServer(h http.Handler) (*httptest.Server, *x509.Certificate) {
	s := httptest.NewUnstartedServer(h)
	s.StartTLS()
	cert, _ := x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])

	return s, cert
}

// below helpers are copied from core/remotes/docker/resolver_test.go
// to avoid circle dependencies
// TODO(djdongjin): move to a testutil package

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

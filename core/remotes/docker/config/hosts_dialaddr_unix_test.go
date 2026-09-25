//go:build !windows

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
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/log/logtest"

	"github.com/containerd/containerd/v2/core/remotes/docker"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// dial_addr is a POSIX-only feature (see hosts_dialaddr_windows.go), so these
// tests live in a !windows file. The Windows counterpart asserts that dial_addr
// is rejected there.

func TestParseHostFileDialAddr(t *testing.T) {
	const testtoml = `
[host."http://uds-pathname.registry"]
  dial_addr = "unix:///run/registry-cache.sock"

[host."http://uds-with-timeout.registry"]
  dial_addr = "unix:///run/r.sock"
  dial_timeout = "2s"
`
	dialTimeout2s := 2 * time.Second
	expected := []hostConfig{
		{
			scheme:       "http",
			host:         "uds-pathname.registry",
			path:         "/v2",
			capabilities: allCaps,
			dialAddr:     "/run/registry-cache.sock",
		},
		{
			scheme:       "http",
			host:         "uds-with-timeout.registry",
			path:         "/v2",
			capabilities: allCaps,
			dialAddr:     "/run/r.sock",
			dialTimeout:  &dialTimeout2s,
		},
	}

	hosts, err := parseHostsFile("", []byte(testtoml))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Log("HostConfigs...\nActual:\n" + printHostConfig(hosts) + "Expected:\n" + printHostConfig(expected))
		}
	}()

	// parseHostsFile appends a trailing default host entry; only the two
	// explicitly configured dial_addr hosts (kept in file order) matter here.
	if len(hosts) < len(expected) {
		t.Fatalf("Unexpected number of hosts %d, expected at least %d", len(hosts), len(expected))
	}
	for i := range expected {
		if !compareHostConfig(hosts[i], expected[i]) {
			t.Fatalf("Mismatch at host %d", i)
		}
	}
}

func TestParseHostFileDialAddrInvalid(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:    "empty after scheme",
			value:   `unix://`,
			wantErr: "no socket path",
		},
		{
			name:    "missing scheme bare path",
			value:   `/run/registry.sock`,
			wantErr: "must start with",
		},
		{
			name:    "tcp scheme",
			value:   `tcp://127.0.0.1:5000`,
			wantErr: "must start with",
		},
		{
			name:    "http scheme",
			value:   `http://localhost`,
			wantErr: "must start with",
		},
		{
			name:    "scheme only no slashes",
			value:   `unix:`,
			wantErr: "must start with",
		},
		{
			name:    "malformed URL",
			value:   `unix://%ZZ`,
			wantErr: "unable to parse",
		},
		{
			name:    "relative path",
			value:   `unix://run/r.sock`,
			wantErr: "must be an absolute path",
		},
		{
			name:    "trailing slash pathname",
			value:   `unix:///run/r.sock/`,
			wantErr: "not a directory",
		},
		{
			name:    "query string",
			value:   `unix:///run/r.sock?foo=bar`,
			wantErr: "query",
		},
		{
			name:    "empty query delimiter",
			value:   `unix:///run/r.sock?`,
			wantErr: "query",
		},
		{
			name:    "fragment",
			value:   `unix:///run/r.sock#frag`,
			wantErr: "fragment",
		},
		{
			name:    "empty fragment delimiter",
			value:   `unix:///run/r.sock#`,
			wantErr: "fragment",
		},
		{
			name:    "abstract empty name",
			value:   `unix://@`,
			wantErr: "not supported",
		},
		{
			name:    "abstract with name",
			value:   `unix://@registry-cache`,
			wantErr: "not supported",
		},
		{
			name:    "leading space",
			value:   ` unix:///run/r.sock`,
			wantErr: "leading or trailing spaces",
		},
		{
			name:    "trailing space",
			value:   `unix:///run/r.sock `,
			wantErr: "leading or trailing spaces",
		},
		{
			name:    "path too long",
			value:   `unix:///` + strings.Repeat("a", 120),
			wantErr: "too long",
		},
		{
			name:    "newline injection",
			value:   "unix:///run/r.sock\nfoo",
			wantErr: "unable to parse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toml := fmt.Sprintf(`[host."http://example.registry"]
  dial_addr = %q
`, tc.value)
			_, err := parseHostsFile("", []byte(toml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestParseDialAddrValid(t *testing.T) {
	// A path at the OS limit is still accepted; one byte over is rejected
	// by TestParseHostFileDialAddrInvalid/path_too_long.
	maxPath := "/" + strings.Repeat("a", maxUnixSocketPathLen-1)

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "pathname",
			value: `unix:///run/r.sock`,
			want:  "/run/r.sock",
		},
		{
			name:  "shortest absolute",
			value: `unix:///a`,
			want:  "/a",
		},
		{
			name:  "uppercase scheme",
			value: `UNIX:///run/r.sock`,
			want:  "/run/r.sock",
		},
		{
			name:  "max length path",
			value: "unix://" + maxPath,
			want:  maxPath,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDialAddr(tc.value)
			if err != nil {
				t.Fatalf("parseDialAddr(%q): unexpected error %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("parseDialAddr(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestParseDialAddrInvalidDirect(t *testing.T) {
	// Inputs TOML cannot carry (e.g. a raw NUL byte) are tested by calling the
	// parser directly. These document that url.Parse rejects control characters.
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "null byte in path", value: "unix:///run/r\x00.sock", wantErr: "unable to parse"},
		{name: "raw newline in path", value: "unix:///run/r.sock\nfoo", wantErr: "unable to parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseDialAddr(tc.value); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("parseDialAddr(%q): want error containing %q, got %v", tc.value, tc.wantErr, err)
			}
		})
	}
}

func TestConfigureHostsDialAddrWiresTransport(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	cases := []struct {
		name                string
		hostToml            string
		wantSharedClient    bool
		wantDialErrContains string
	}{
		{
			name: "only dial_addr set",
			hostToml: `
[host."http://uds.registry"]
  dial_addr = "unix:///nonexistent-uds-test.sock"
`,
			wantDialErrContains: "nonexistent-uds-test.sock",
		},
		{
			name: "dial_addr with dial_timeout",
			hostToml: `
[host."http://uds.registry"]
  dial_addr = "unix:///nonexistent-uds-timeout.sock"
  dial_timeout = "100ms"
`,
			wantDialErrContains: "nonexistent-uds-timeout.sock",
		},
		{
			name: "neither set (control)",
			hostToml: `
[host."http://plain.registry"]
`,
			wantSharedClient: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rhosts := configureFromHostToml(t, ctx, tc.hostToml)
			if tc.wantSharedClient {
				assertClientSharedBetweenHosts(t, rhosts)
				return
			}
			assertDialerTargetsUnixSocket(t, ctx, rhosts[0].Client, tc.wantDialErrContains)
		})
	}
}

func configureFromHostToml(t *testing.T, ctx context.Context, hostToml string) []docker.RegistryHost {
	t.Helper()
	hostDir := filepath.Join(t.TempDir(), "example.registry")
	if err := os.MkdirAll(hostDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostToml), 0600); err != nil {
		t.Fatal(err)
	}
	opts := HostOptions{HostDir: func(string) (string, error) { return hostDir, nil }}
	rhosts, err := ConfigureHosts(ctx, opts)("example.registry")
	if err != nil {
		t.Fatalf("ConfigureHosts: %v", err)
	}
	if len(rhosts) == 0 {
		t.Fatal("expected at least one host")
	}
	return rhosts
}

func assertClientSharedBetweenHosts(t *testing.T, rhosts []docker.RegistryHost) {
	t.Helper()
	if len(rhosts) < 2 {
		t.Fatalf("expected at least 2 hosts to compare clients, got %d", len(rhosts))
	}
	if rhosts[0].Client != rhosts[1].Client {
		t.Errorf("expected shared *http.Client across hosts without dial_addr, got distinct clients")
	}
}

func assertDialerTargetsUnixSocket(t *testing.T, ctx context.Context, client *http.Client, wantErrSubstr string) {
	t.Helper()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("expected per-host DialContext to be set")
	}
	// A dial_addr host always connects directly to the socket, so the
	// transport must not route via HTTP(S)_PROXY (Proxy must be cleared).
	if tr.Proxy != nil {
		t.Error("expected Proxy to be nil for a dial_addr host")
	}
	_, derr := tr.DialContext(ctx, "tcp", "ignored:443")
	if derr == nil {
		t.Fatal("expected dial to a nonexistent unix socket to fail")
	}
	if !strings.Contains(derr.Error(), wantErrSubstr) {
		t.Errorf("expected dial error to mention %q, got %q", wantErrSubstr, derr.Error())
	}
}

// TestResolverDialAddrUnixSocket is an integration test: it serves a registry
// on a real unix socket and checks that a host with dial_addr resolves over the
// socket, not over TCP. dial_addr changes only the dial, so the host stays a
// plain http:// entry.
func TestResolverDialAddrUnixSocket(t *testing.T) {
	const (
		name = "testname"
		tag  = "latest"
		base = "dial-addr-uds.registry"
	)

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("1")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("2")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())
	mux := http.NewServeMux()
	m.RegisterHandler(mux, name)
	mux.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, tag), mc)
	mux.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)

	var hits atomic.Int64
	counted := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mux.ServeHTTP(rw, r)
	})

	sock := filepath.Join(t.TempDir(), "reg.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		// Skip rather than fail where AF_UNIX is unavailable, matching how
		// stdlib's own unix-socket tests behave (EAFNOSUPPORT text).
		if strings.Contains(err.Error(), "address family not supported") {
			t.Skipf("unix sockets not supported in this environment: %v", err)
		}
		t.Fatalf("listen unix %q: %v", sock, err)
	}
	defer l.Close()
	srv := &http.Server{Handler: counted}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()
	defer func() {
		srv.Close()
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("unix socket server: %v", err)
		}
	}()

	resolveWith := func(t *testing.T, sock string) error {
		dir := t.TempDir()
		hostDir := filepath.Join(dir, base)
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatal(err)
		}
		hostTOML := fmt.Sprintf(`
[host."http://%s"]
  capabilities = ["pull", "resolve"]
  dial_addr = "unix://%s"
`, base, sock)
		if err := os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostTOML), 0644); err != nil {
			t.Fatal(err)
		}
		options := docker.ResolverOptions{
			Hosts: ConfigureHosts(context.TODO(), HostOptions{HostDir: HostDirFromRoot(dir)}),
		}
		resolver := docker.NewResolver(options)
		_, _, err := resolver.Resolve(context.Background(), fmt.Sprintf("%s/%s:%s", base, name, tag))
		return err
	}

	t.Run("served over socket", func(t *testing.T) {
		before := hits.Load()
		if err := resolveWith(t, sock); err != nil {
			t.Fatalf("resolve over unix socket: %v", err)
		}
		if hits.Load() <= before {
			t.Fatal("expected the registry handler to be reached over the unix socket")
		}
	})

	t.Run("bogus socket fails", func(t *testing.T) {
		bogus := filepath.Join(t.TempDir(), "nonexistent.sock")
		if err := resolveWith(t, bogus); err == nil {
			t.Fatal("expected resolve to fail when dial_addr points at a nonexistent socket")
		}
	})
}

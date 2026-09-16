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
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestRegistryHostPaths(t *testing.T) {
	const manifest = `{"schemaVersion":2,"config":{},"layers":[]}`
	manifestDigest := digest.FromString(manifest)
	for _, serialized := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			roots      int
			configured int
			host       string
			separator  bool
		}{
			{name: "single root", roots: 1, host: "registry.invalid"},
			{name: "literal separator", roots: 1, host: "registry.invalid", separator: true},
			{name: "first root wins", roots: 2, host: "registry.invalid"},
			{name: "second root", roots: 2, configured: 1, host: "registry.invalid"},
			{name: "first root default wins", roots: 2, host: "_default"},
		} {
			t.Run(fmt.Sprintf("serialized=%t/%s", serialized, tc.name), func(t *testing.T) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v2/test/image/manifests/latest" || r.Header.Get("Authorization") != "Bearer test-token" {
						t.Errorf("unexpected registry request: %s, authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
					w.Header().Set("Content-Length", fmt.Sprint(len(manifest)))
					w.Header().Set("Docker-Content-Digest", manifestDigest.String())
					if r.Method != http.MethodHead {
						_, _ = w.Write([]byte(manifest))
					}
				}))
				defer server.Close()

				roots := make([]string, tc.roots)
				for i := range roots {
					roots[i] = t.TempDir()
					if tc.separator {
						roots[i] = filepath.Join(roots[i], "certs"+string(os.PathListSeparator)+"literal")
					}
				}
				hostDir := filepath.Join(roots[tc.configured], tc.host)
				require.NoError(t, os.MkdirAll(hostDir, 0700))
				ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
				require.NoError(t, os.WriteFile(filepath.Join(hostDir, "ca.pem"), ca, 0600))
				require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), fmt.Appendf(nil,
					"server = %q\nca = 'ca.pem'\n[header]\nauthorization = 'Bearer test-token'\n", server.URL), 0600))
				if tc.roots > 1 && tc.configured == 0 {
					// A later root must not override either a host entry or an earlier _default.
					other := filepath.Join(roots[1], "registry.invalid")
					require.NoError(t, os.MkdirAll(other, 0700))
					require.NoError(t, os.WriteFile(filepath.Join(other, "hosts.toml"), []byte("server = 'https://127.0.0.1:1'\n"), 0600))
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				opts := []Opt{WithHostDir("")}
				for _, root := range roots {
					opts = append(opts, WithHostDir(root), WithHostDir(""))
				}
				registry, err := NewOCIRegistry(ctx, "registry.invalid/test/image:latest", opts...)
				require.NoError(t, err)
				if serialized {
					encoded, err := registry.MarshalAny(ctx, nil)
					require.NoError(t, err)
					var wire transfertypes.OCIRegistry
					require.NoError(t, typeurl.UnmarshalTo(encoded, &wire))
					require.Equal(t, roots[0], wire.Resolver.HostDir)
					require.Equal(t, roots[1:], append([]string{}, wire.Resolver.HostDirs...))
					registry = &OCIRegistry{}
					require.NoError(t, registry.UnmarshalAny(ctx, nil, encoded))
					encoded, err = registry.MarshalAny(ctx, nil)
					require.NoError(t, err)
					registry = &OCIRegistry{}
					require.NoError(t, registry.UnmarshalAny(ctx, nil, encoded))
				}
				_, desc, err := registry.Resolve(ctx)
				require.NoError(t, err)
				require.Equal(t, manifestDigest, desc.Digest)
				require.Equal(t, int64(len(manifest)), desc.Size)
			})
		}
	}
}

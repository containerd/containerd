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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestWithHostDir_AppendsInCallOrder(t *testing.T) {
	var ropts registryOpts

	if err := WithHostDir("")(&ropts); err != nil {
		t.Fatalf("WithHostDir(empty): %v", err)
	}
	if err := WithHostDir("/etc/containerd/certs.d")(&ropts); err != nil {
		t.Fatalf("WithHostDir(first): %v", err)
	}
	if err := WithHostDir("/etc/docker/certs.d")(&ropts); err != nil {
		t.Fatalf("WithHostDir(second): %v", err)
	}

	want := []string{"/etc/containerd/certs.d", "/etc/docker/certs.d"}
	if !reflect.DeepEqual(ropts.hostDirs, want) {
		t.Fatalf("unexpected hostDirs: got %v, want %v", ropts.hostDirs, want)
	}
}

func TestOCIRegistryHostDirsRoundTrip(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	manifestDigest := digest.FromBytes(manifest)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Test-Host-Dir") != "configured" {
			http.Error(writer, "host directory configuration missing", http.StatusForbidden)
			return
		}
		writer.Header().Set("Content-Type", imagespec.MediaTypeImageManifest)
		writer.Header().Set("Content-Length", fmt.Sprint(len(manifest)))
		writer.Header().Set("Docker-Content-Digest", manifestDigest.String())
		if request.Method != http.MethodHead {
			_, _ = writer.Write(manifest)
		}
	}))
	t.Cleanup(server.Close)
	reference := strings.TrimPrefix(server.URL, "http://") + "/test:latest"

	for _, testCase := range []struct {
		name  string
		roots []string
	}{
		{name: "single", roots: []string{t.TempDir()}},
		{name: "multiple", roots: []string{t.TempDir(), t.TempDir()}},
		{name: "literal separator", roots: []string{filepath.Join(t.TempDir(), "first"+string(os.PathListSeparator)+"second")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostDir := filepath.Join(testCase.roots[len(testCase.roots)-1], "_default")
			if err := os.MkdirAll(hostDir, 0o755); err != nil {
				t.Fatal(err)
			}
			configuration := fmt.Sprintf("server = %q\n[header]\n  X-Test-Host-Dir = [\"configured\"]\n", server.URL)
			if err := os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(configuration), 0o644); err != nil {
				t.Fatal(err)
			}

			opts := []Opt{WithHostDir("")}
			for _, root := range testCase.roots {
				opts = append(opts, WithHostDir(root), WithHostDir(""))
			}
			registry, err := NewOCIRegistry(t.Context(), reference, opts...)
			if err != nil {
				t.Fatal(err)
			}
			for roundTrip := range 3 {
				_, descriptor, err := registry.Resolve(t.Context())
				if err != nil {
					t.Fatalf("resolve after %d round trips: %v", roundTrip, err)
				}
				if descriptor.Digest != manifestDigest {
					t.Fatalf("unexpected digest: got %s, want %s", descriptor.Digest, manifestDigest)
				}

				encoded, err := registry.MarshalAny(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				var wire transfertypes.OCIRegistry
				if err := typeurl.UnmarshalTo(encoded, &wire); err != nil {
					t.Fatal(err)
				}
				if wire.Resolver.HostDir != testCase.roots[0] {
					t.Fatalf("first host directory after %d round trips: got %q, want %q", roundTrip, wire.Resolver.HostDir, testCase.roots[0])
				}
				if !slices.Equal(wire.Resolver.HostDirs, testCase.roots[1:]) {
					t.Fatalf("additional host directories after %d round trips: got %v, want %v", roundTrip, wire.Resolver.HostDirs, testCase.roots[1:])
				}
				registry = &OCIRegistry{}
				if err := registry.UnmarshalAny(t.Context(), nil, encoded); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOCIRegistryUnmarshalHostDirs(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		resolver *transfertypes.RegistryResolver
		want     []string
	}{
		{name: "no resolver"},
		{name: "empty resolver", resolver: &transfertypes.RegistryResolver{}},
		{name: "legacy", resolver: &transfertypes.RegistryResolver{HostDir: "first"}, want: []string{"first"}},
		{name: "multiple", resolver: &transfertypes.RegistryResolver{HostDir: "first", HostDirs: []string{"second", "third"}}, want: []string{"first", "second", "third"}},
		{name: "empty entries", resolver: &transfertypes.RegistryResolver{HostDirs: []string{"", "second", ""}}, want: []string{"second"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := typeurl.MarshalAny(&transfertypes.OCIRegistry{
				Reference: "registry.example.com/test:latest",
				Resolver:  testCase.resolver,
			})
			if err != nil {
				t.Fatal(err)
			}
			registry := &OCIRegistry{hostDirs: []string{"stale"}}
			if err := registry.UnmarshalAny(t.Context(), nil, encoded); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(registry.hostDirs, testCase.want) {
				t.Fatalf("unexpected host directories: got %v, want %v", registry.hostDirs, testCase.want)
			}
		})
	}
}

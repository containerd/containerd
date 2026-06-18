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
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestOCIRegistryUnmarshalAnyHostDirList checks that a host directory list, as
// accepted by WithHostDir, is also honored on the receiving (server) side of
// the transfer service, where the resolver is rebuilt from the marshaled type.
func TestOCIRegistryUnmarshalAnyHostDirList(t *testing.T) {
	const host = "testhost.local"

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","size":0},"layers":[]}`)
	dgst := digest.FromBytes(manifest)

	var requested bool
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/testname/manifests/latest" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		requested = true
		rw.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
		rw.Header().Set("Docker-Content-Digest", dgst.String())
		rw.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifest)))
		rw.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			rw.Write(manifest)
		}
	}))
	defer srv.Close()

	// Only the second root holds a configuration for the host, so the whole
	// list must be searched for the registry to be found.
	first, second := t.TempDir(), t.TempDir()
	hostDirName := host
	if runtime.GOOS == "windows" {
		hostDirName = strings.ReplaceAll(hostDirName, ":", "")
	}
	hostDir := filepath.Join(second, hostDirName)
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatal(err)
	}
	hostTOML := fmt.Sprintf("server = %q\n\n[host.%q]\n  capabilities = [\"pull\", \"resolve\"]\n", srv.URL, srv.URL)
	if err := os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostTOML), 0644); err != nil {
		t.Fatal(err)
	}

	ref := host + "/testname:latest"
	a, err := typeurl.MarshalAny(&transfertypes.OCIRegistry{
		Reference: ref,
		Resolver: &transfertypes.RegistryResolver{
			HostDir: strings.Join([]string{first, second}, string(os.PathListSeparator)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var r OCIRegistry
	if err := r.UnmarshalAny(ctx, nil, a); err != nil {
		t.Fatal(err)
	}

	if r.hostDir != strings.Join([]string{first, second}, string(os.PathListSeparator)) {
		t.Errorf("unexpected host dir %q", r.hostDir)
	}

	_, desc, err := r.resolver.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", ref, err)
	}
	if !requested {
		t.Fatal("configured registry was not used")
	}
	if desc.Digest != dgst {
		t.Errorf("unexpected digest %s, expected %s", desc.Digest, dgst)
	}
}

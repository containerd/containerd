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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/errdefs"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestFetchReferrers(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		runReferrersTest(t, "testname", tlsServer)
	})
	t.Run("missing length", func(t *testing.T) {
		runReferrersTest(t, "testname", tlsServer, func(tc *testContent) {
			tc.skipLength = true
		})
	})
	t.Run("too long", func(t *testing.T) {
		runReferrersTest(t, "testname", tlsServer, func(tc *testContent) {
			tc.content = make([]byte, MaxManifestSize+1)
		})
	})
}

func runReferrersTest(t *testing.T, name string, sf func(h http.Handler) (string, ResolverOptions, func()), ropts ...contentOpt) {
	var (
		ctx = context.Background()
		r   = http.NewServeMux()
	)

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("1")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("2")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())

	i := newIndex(
		newContent(ocispec.MediaTypeImageManifest, []byte("some signature manifest"), withArtifactType("application/vnd.test.sig")),
		newContent(ocispec.MediaTypeImageManifest, []byte("some sbom"), withArtifactType("application/vnd.test.sbom")),
	)
	ic := newContent(ocispec.MediaTypeImageIndex, i.OCIManifest(), ropts...)

	m.RegisterHandler(r, name)
	i.RegisterHandler(r, name)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)
	r.Handle(fmt.Sprintf("/v2/%s/referrers/%s", name, mc.Digest()), ic)
	r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, strings.Replace(mc.Digest().String(), ":", "-", 1)), ic)

	base, ro, close := sf(logHandler{t, r})
	defer close()

	resolver := NewResolver(ro)
	image := fmt.Sprintf("%s/%s@%s", base, name, mc.Digest())

	_, d, err := resolver.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	f, err := resolver.Fetcher(ctx, image)
	if err != nil {
		t.Fatal(err)
	}

	rf := f.(remotes.ReferrersFetcher)

	refs, err := rf.FetchReferrers(ctx, d.Digest)
	if len(ic.content) > int(MaxManifestSize) {
		if err == nil {
			t.Fatal("expected error for exceeding max size")
		}
		if !strings.Contains(err.Error(), "exceeds maximum allowed") {
			t.Fatalf("unexpected error: %v", err)
		}
		if !errdefs.IsNotFound(err) {
			t.Fatalf("unexpected error type: %v", err)
		}
		return
	}
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

	refs, err = rf.FetchReferrers(ctx, d.Digest, remotes.WithReferrerArtifactTypes("application/vnd.test.sig"))
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 1 {
		t.Fatalf("Unexpected number of references: %d, expected 1", len(refs))
	}

	for _, ref := range refs {
		if ref.ArtifactType != "application/vnd.test.sig" {
			t.Fatalf("Unexpected artifact type: %q", ref.ArtifactType)
		}
	}
}

type testIndex struct {
	manifests []testContent
}

func newIndex(manifests ...testContent) testIndex {
	return testIndex{
		manifests: manifests,
	}
}

func (ti testIndex) OCIManifest() []byte {
	manifest := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Manifests: make([]ocispec.Descriptor, len(ti.manifests)),
	}
	for i, c := range ti.manifests {
		manifest.Manifests[i] = c.Descriptor()
	}
	b, _ := json.Marshal(manifest)
	return b
}

func (ti testIndex) RegisterHandler(r *http.ServeMux, name string) {
	for _, c := range ti.manifests {
		r.Handle(fmt.Sprintf("/v2/%s/blobs/%s", name, c.Digest()), c)
		r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, c.Digest()), c)
	}
}

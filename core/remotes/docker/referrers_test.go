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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/remotes"
	remoteerrors "github.com/containerd/containerd/v2/core/remotes/errors"
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

func TestFetchReferrersFallbackBadRequest(t *testing.T) {
	for _, tc := range []struct {
		name           string
		apiStatus      int
		fallbackStatus int
		wantStatus     int
	}{
		{"invalid fallback index", http.StatusNotFound, http.StatusBadRequest, 0},
		{"fallback forbidden", http.StatusNotFound, http.StatusForbidden, http.StatusForbidden},
		{"API forbidden", http.StatusForbidden, http.StatusBadRequest, http.StatusForbidden},
		{"API forbidden with missing tag", http.StatusForbidden, http.StatusNotFound, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const name = "testname"
			mc := newContent(ocispec.MediaTypeImageManifest, []byte("{}"))
			r := http.NewServeMux()
			r.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), mc)
			r.HandleFunc(fmt.Sprintf("/v2/%s/referrers/%s", name, mc.Digest()), func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.apiStatus)
			})
			r.HandleFunc(fmt.Sprintf("/v2/%s/manifests/%s", name, strings.Replace(mc.Digest().String(), ":", "-", 1)), func(w http.ResponseWriter, _ *http.Request) {
				if tc.fallbackStatus == http.StatusBadRequest {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tc.fallbackStatus)
				if tc.fallbackStatus == http.StatusBadRequest {
					fmt.Fprint(w, `{"errors":[{"code":"MANIFEST_INVALID","message":"Schema 2 manifest not supported by client"}]}`)
				}
			})

			base, ro, close := tlsServer(logHandler{t, r})
			defer close()
			image := fmt.Sprintf("%s/%s@%s", base, name, mc.Digest())
			resolver := NewResolver(ro)
			_, desc, err := resolver.Resolve(ctx, image)
			if err != nil {
				t.Fatal(err)
			}
			f, err := resolver.Fetcher(ctx, image)
			if err != nil {
				t.Fatal(err)
			}
			refs, err := f.(remotes.ReferrersFetcher).FetchReferrers(ctx, desc.Digest)
			if tc.wantStatus == 0 {
				if err != nil || len(refs) != 0 {
					t.Fatalf("expected no referrers, got %v, %v", refs, err)
				}
				return
			}
			var status remoteerrors.ErrUnexpectedStatus
			if !errors.As(err, &status) || status.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %v", tc.wantStatus, err)
			}
		})
	}
}

func TestFetchReferrersFallbackHosts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		firstStatus  int
		secondStatus int
	}{
		{"missing tag on first host", http.StatusNotFound, http.StatusOK},
		{"rejected tag on first host", http.StatusBadRequest, http.StatusOK},
		{"later host error", http.StatusBadRequest, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			const name = "testname"
			mc := newContent(ocispec.MediaTypeImageManifest, []byte("{}"))
			referrer := newContent(ocispec.MediaTypeImageManifest, []byte("referrer"))
			ic := newContent(ocispec.MediaTypeImageIndex, newIndex(referrer).OCIManifest())
			tag := strings.Replace(mc.Digest().String(), ":", "-", 1)
			r := http.NewServeMux()
			for _, path := range []string{"/mirror/v2", "/v2"} {
				r.Handle(path+"/"+name+"/manifests/"+mc.Digest().String(), mc)
				r.HandleFunc(path+"/"+name+"/referrers/"+mc.Digest().String(), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
			}
			r.HandleFunc("/mirror/v2/"+name+"/manifests/"+tag, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.firstStatus)
			})
			r.HandleFunc("/v2/"+name+"/manifests/"+tag, func(w http.ResponseWriter, req *http.Request) {
				if tc.secondStatus == http.StatusOK {
					ic.ServeHTTP(w, req)
				} else {
					w.WriteHeader(tc.secondStatus)
				}
			})

			base, ro, close := tlsServer(logHandler{t, r})
			defer close()
			ro.Hosts = func(string) ([]RegistryHost, error) {
				var hosts []RegistryHost
				for _, path := range []string{"/mirror/v2", "/v2"} {
					hosts = append(hosts, RegistryHost{
						Client: ro.Client, Host: base, Scheme: "https", Path: path,
						Capabilities: HostCapabilityPull | HostCapabilityResolve | HostCapabilityReferrers,
					})
				}
				return hosts, nil
			}
			image := fmt.Sprintf("%s/%s@%s", base, name, mc.Digest())
			resolver := NewResolver(ro)
			_, desc, err := resolver.Resolve(ctx, image)
			if err != nil {
				t.Fatal(err)
			}
			f, err := resolver.Fetcher(ctx, image)
			if err != nil {
				t.Fatal(err)
			}
			refs, err := f.(remotes.ReferrersFetcher).FetchReferrers(ctx, desc.Digest)
			if tc.secondStatus == http.StatusOK {
				if err != nil || len(refs) != 1 || refs[0].Digest != referrer.Digest() {
					t.Fatalf("expected referrer from later host, got %v, %v", refs, err)
				}
				return
			}
			var status remoteerrors.ErrUnexpectedStatus
			if !errors.As(err, &status) || status.StatusCode != tc.secondStatus {
				t.Fatalf("expected status %d from later host, got %v", tc.secondStatus, err)
			}
		})
	}
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

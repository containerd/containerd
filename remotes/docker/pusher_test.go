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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/containerd/containerd/content"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestGetManifestPath(t *testing.T) {
	for _, tc := range []struct {
		object   string
		dgst     digest.Digest
		expected []string
	}{
		{
			object:   "foo",
			dgst:     "bar",
			expected: []string{"manifests", "foo"},
		},
		{
			object:   "foo@bar",
			dgst:     "bar",
			expected: []string{"manifests", "foo"},
		},
		{
			object:   "foo@bar",
			dgst:     "foobar",
			expected: []string{"manifests", "foobar"},
		},
	} {
		if got := getManifestPath(tc.object, tc.dgst); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}

// TestPusherErrClosedRetry tests if retrying work when error occurred on close.
func TestPusherErrClosedRetry(t *testing.T) {
	ctx := context.Background()

	p, reg, done := samplePusher(t)
	defer done()

	layerContent := []byte("test")
	reg.uploadable = false
	if err := tryUpload(ctx, t, p, layerContent); err == nil {
		t.Errorf("upload should fail but succeeded")
	}

	// retry
	reg.uploadable = true
	if err := tryUpload(ctx, t, p, layerContent); err != nil {
		t.Errorf("upload should succeed but got %v", err)
	}
}

func tryUpload(ctx context.Context, t *testing.T, p dockerPusher, layerContent []byte) error {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(layerContent),
		Size:      int64(len(layerContent)),
	}
	cw, err := p.Writer(ctx, content.WithRef("test-1"), content.WithDescriptor(desc))
	if err != nil {
		return err
	}
	defer cw.Close()
	if _, err := cw.Write(layerContent); err != nil {
		return err
	}
	return cw.Commit(ctx, 0, "")
}

func samplePusher(t *testing.T) (dockerPusher, *uploadableMockRegistry, func()) {
	reg := &uploadableMockRegistry{}
	s := httptest.NewServer(reg)
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return dockerPusher{
		dockerBase: &dockerBase{
			repository: "sample",
			hosts: []RegistryHost{
				{
					Client:       s.Client(),
					Host:         u.Host,
					Scheme:       u.Scheme,
					Path:         u.Path,
					Capabilities: HostCapabilityPush | HostCapabilityResolve,
				},
			},
		},
		object:  "sample",
		tracker: NewInMemoryTracker(),
	}, reg, s.Close
}

var manifestRegexp = regexp.MustCompile(`/([a-z0-9]+)/manifests/(.*)`)
var blobUploadRegexp = regexp.MustCompile(`/([a-z0-9]+)/blobs/uploads/`)

// uploadableMockRegistry provides minimal registry APIs which are enough to serve requests from dockerPusher.
type uploadableMockRegistry struct {
	uploadable bool
}

func (u *uploadableMockRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if matches := blobUploadRegexp.FindStringSubmatch(r.URL.Path); len(matches) != 0 {
			if u.uploadable {
				w.Header().Set("Location", "/upload")
			} else {
				w.Header().Set("Location", "/cannotupload")
			}
			w.WriteHeader(202)
			return
		}
	} else if r.Method == "PUT" {
		mfstMatches := manifestRegexp.FindStringSubmatch(r.URL.Path)
		if len(mfstMatches) != 0 || strings.HasPrefix(r.URL.Path, "/upload") {
			dgstr := digest.Canonical.Digester()
			if _, err := io.Copy(dgstr.Hash(), r.Body); err != nil {
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Docker-Content-Digest", dgstr.Digest().String())
			w.WriteHeader(201)
			return
		} else if r.URL.Path == "/cannotupload" {
			w.WriteHeader(500)
			return
		}
	}
	fmt.Println(r)
	w.WriteHeader(404)
}

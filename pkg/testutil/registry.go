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

package testutil

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ServeImage serves an image from a read-only HTTP registry listening on
// localhost, and returns a reference to pull the image from.
//
// blobs must be keyed by digest and contain the manifest blob referenced by
// target, along with the blobs it references, except for those already
// present in the puller's content store. The registry is shut down when the
// test completes.
func ServeImage(t *testing.T, name, tag string, target ocispec.Descriptor, blobs map[digest.Digest][]byte) string {
	manifestPath := "/v2/" + name + "/manifests/"
	blobPath := "/v2/" + name + "/blobs/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var b []byte
		mediaType := "application/octet-stream"
		switch {
		case strings.HasPrefix(r.URL.Path, manifestPath):
			// A manifest may be requested by the tag, by the target
			// digest, or, for a multi-platform image, by the digest of
			// a platform manifest.
			dgst := target.Digest
			if ref := strings.TrimPrefix(r.URL.Path, manifestPath); ref != tag {
				var err error
				if dgst, err = digest.Parse(ref); err != nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
			var ok bool
			if b, ok = blobs[dgst]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if dgst == target.Digest {
				mediaType = target.MediaType
			}
			w.Header().Set("Docker-Content-Digest", dgst.String())
		case strings.HasPrefix(r.URL.Path, blobPath):
			dgst, err := digest.Parse(strings.TrimPrefix(r.URL.Path, blobPath))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var ok bool
			if b, ok = blobs[dgst]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://") + "/" + name + ":" + tag
}

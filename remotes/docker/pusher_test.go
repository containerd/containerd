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
	"errors"
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
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
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

// TestPusherErrReset tests the push method if the request needs to be retried
// i.e when ErrReset occurs
func TestPusherErrReset(t *testing.T) {
	p, reg, done := samplePusher(t)
	defer done()

	p.object = "latest@sha256:55d31f3af94c797b65b310569803cacc1c9f4a34bf61afcdc8138f89345c8308"

	reg.uploadable = true
	reg.putHandlerFunc = func() func(w http.ResponseWriter, r *http.Request) bool {
		// sets whether the request should timeout so that a reset error can occur and
		// request will be retried
		shouldTimeout := true
		return func(w http.ResponseWriter, r *http.Request) bool {
			if shouldTimeout {
				shouldTimeout = !shouldTimeout
				w.WriteHeader(http.StatusRequestTimeout)
				return true
			}
			return false
		}
	}()

	ct := []byte("manifest-content")

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(ct),
		Size:      int64(len(ct)),
	}

	w, err := p.push(context.Background(), desc, remotes.MakeRefKey(context.Background(), desc), false)
	assert.NoError(t, err)

	// first push should fail with ErrReset
	_, err = w.Write(ct)
	assert.NoError(t, err)
	err = w.Commit(context.Background(), desc.Size, desc.Digest)
	assert.Equal(t, content.ErrReset, err)

	// second push should succeed
	_, err = w.Write(ct)
	assert.NoError(t, err)
	err = w.Commit(context.Background(), desc.Size, desc.Digest)
	assert.NoError(t, err)
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
	reg := &uploadableMockRegistry{
		availableContents: make([]string, 0),
	}
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
var blobUploadRegexp = regexp.MustCompile(`/([a-z0-9]+)/blobs/uploads/(.*)`)

// uploadableMockRegistry provides minimal registry APIs which are enough to serve requests from dockerPusher.
type uploadableMockRegistry struct {
	availableContents []string
	uploadable        bool
	putHandlerFunc    func(w http.ResponseWriter, r *http.Request) bool
}

func (u *uploadableMockRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && u.putHandlerFunc != nil {
		// if true return the response witout calling default handler
		if u.putHandlerFunc(w, r) {
			return
		}
	}
	u.defaultHandler(w, r)
}

func (u *uploadableMockRegistry) defaultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if matches := blobUploadRegexp.FindStringSubmatch(r.URL.Path); len(matches) != 0 {
			if u.uploadable {
				w.Header().Set("Location", "/upload")
			} else {
				w.Header().Set("Location", "/cannotupload")
			}
			dgstr := digest.Canonical.Digester()
			if _, err := io.Copy(dgstr.Hash(), r.Body); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			u.availableContents = append(u.availableContents, dgstr.Digest().String())
			w.WriteHeader(http.StatusAccepted)
			return
		}
	} else if r.Method == http.MethodPut {
		mfstMatches := manifestRegexp.FindStringSubmatch(r.URL.Path)
		if len(mfstMatches) != 0 || strings.HasPrefix(r.URL.Path, "/upload") {
			dgstr := digest.Canonical.Digester()
			if _, err := io.Copy(dgstr.Hash(), r.Body); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			u.availableContents = append(u.availableContents, dgstr.Digest().String())
			w.Header().Set("Docker-Content-Digest", dgstr.Digest().String())
			w.WriteHeader(http.StatusCreated)
			return
		} else if r.URL.Path == "/cannotupload" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	} else if r.Method == http.MethodHead {
		var content string
		// check for both manifest and blob paths
		if manifestMatch := manifestRegexp.FindStringSubmatch(r.URL.Path); len(manifestMatch) == 3 {
			content = manifestMatch[2]
		} else if blobMatch := blobUploadRegexp.FindStringSubmatch(r.URL.Path); len(blobMatch) == 3 {
			content = blobMatch[2]
		}
		// if content is not found or if the path is not manifest or blob
		// we return 404
		if u.isContentAlreadyExist(content) {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	fmt.Println(r)
	w.WriteHeader(http.StatusNotFound)
}

// checks if the content is already present in the registry
func (u *uploadableMockRegistry) isContentAlreadyExist(c string) bool {
	for _, ct := range u.availableContents {
		if ct == c {
			return true
		}
	}
	return false
}

func Test_dockerPusher_push(t *testing.T) {

	p, reg, done := samplePusher(t)
	defer done()

	reg.uploadable = true

	manifestContent := []byte("manifest-content")
	manifestContentDigest := digest.FromBytes(manifestContent)
	layerContent := []byte("layer-content")
	layerContentDigest := digest.FromBytes(layerContent)

	// using a random object here
	baseObject := "latest@sha256:55d31f3af94c797b65b310569803cacc1c9f4a34bf61afcdc8138f89345c8308"

	type args struct {
		content           []byte
		mediatype         string
		ref               string
		unavailableOnFail bool
	}
	tests := []struct {
		name             string
		dp               dockerPusher
		dockerBaseObject string
		args             args
		checkerFunc      func(writer *pushWriter) bool
		wantErr          error
	}{
		{
			name:             "when a manifest is pushed",
			dp:               p,
			dockerBaseObject: baseObject,
			args: args{
				content:           manifestContent,
				mediatype:         ocispec.MediaTypeImageManifest,
				ref:               fmt.Sprintf("manifest-%s", manifestContentDigest.String()),
				unavailableOnFail: false,
			},
			checkerFunc: func(writer *pushWriter) bool {
				select {
				case resp := <-writer.respC:
					// 201 should be the response code when uploading a new manifest
					return resp.StatusCode == http.StatusCreated
				case <-writer.errC:
					return false
				}
			},
			wantErr: nil,
		},
		{
			name:             "trying to push content that already exists",
			dp:               p,
			dockerBaseObject: baseObject,
			args: args{
				content:           manifestContent,
				mediatype:         ocispec.MediaTypeImageManifest,
				ref:               fmt.Sprintf("manifest-%s", manifestContentDigest.String()),
				unavailableOnFail: false,
			},
			wantErr: fmt.Errorf("content %v on remote: %w", digest.FromBytes(manifestContent), errdefs.ErrAlreadyExists),
		},
		{
			name: "trying to push a blob layer",
			dp:   p,
			// Not needed to set the base object as it is used to generate path only in case of manifests
			// dockerBaseObject:
			args: args{
				content:           layerContent,
				mediatype:         ocispec.MediaTypeImageLayer,
				ref:               fmt.Sprintf("layer-%s", layerContentDigest.String()),
				unavailableOnFail: false,
			},
			checkerFunc: func(writer *pushWriter) bool {
				select {
				case resp := <-writer.respC:
					// 201 should be the response code when uploading a new blob
					return resp.StatusCode == http.StatusCreated
				case <-writer.errC:
					return false
				}
			},
			wantErr: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desc := ocispec.Descriptor{
				MediaType: test.args.mediatype,
				Digest:    digest.FromBytes(test.args.content),
				Size:      int64(len(test.args.content)),
			}

			test.dp.object = test.dockerBaseObject

			got, err := test.dp.push(context.Background(), desc, test.args.ref, test.args.unavailableOnFail)

			assert.Equal(t, test.wantErr, err)
			// if an error is expected, further comparisons are not required.
			if test.wantErr != nil {
				return
			}

			// write the content to the writer, this will be done when a Read() is called on the body of the request
			got.Write(test.args.content)

			pw, ok := got.(*pushWriter)
			if !ok {
				assert.Errorf(t, errors.New("unable to cast content.Writer to pushWriter"), "got %v instead of pushwriter", got)
			}

			// test whether a proper response has been received after the push operation
			assert.True(t, test.checkerFunc(pw))

		})
	}
}

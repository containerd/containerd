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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/errdefs"
	"github.com/containerd/log/logtest"
	"github.com/opencontainers/go-digest"
	ocispecv "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	p, reg, _, done := samplePusher(t)
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

func TestPusherHTTPFallback(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	p, reg, _, done := samplePusher(t)
	defer done()

	reg.uploadable = true
	reg.username = "testuser"
	reg.secret = "testsecret"
	reg.locationPrefix = p.hosts[0].Scheme + "://" + p.hosts[0].Host

	p.hosts[0].Scheme = "https"
	client := p.hosts[0].Client
	if client == nil {
		clientC := *http.DefaultClient
		client = &clientC
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	client.Transport = NewHTTPFallback(client.Transport)
	p.hosts[0].Client = client
	phost := p.hosts[0].Host
	p.hosts[0].Authorizer = NewDockerAuthorizer(WithAuthCreds(func(host string) (string, string, error) {
		if host == phost {
			return "testuser", "testsecret", nil
		}
		return "", "", nil
	}))

	layerContent := []byte("test")
	if err := tryUpload(ctx, t, p, layerContent); err != nil {
		t.Errorf("upload failed: %v", err)
	}
}

// TestPusherErrReset tests the push method if the request needs to be retried
// i.e when ErrReset occurs
func TestPusherErrReset(t *testing.T) {
	p, reg, _, done := samplePusher(t)
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

func TestPusherInvalidAuthorizationOnMount(t *testing.T) {
	t.Parallel()
	// Simulate trying to mount a private repo that we cannot access to

	isCrossRepoMount := func(r *http.Request) bool {
		return r.URL.Query().Get("mount") != ""
	}

	testCases := []struct {
		name  string
		setup func(t *testing.T, p *dockerPusher, reg *uploadableMockRegistry, triggered func())
	}{
		{
			name: "Authorizer.Authorize error",
			setup: func(t *testing.T, p *dockerPusher, _ *uploadableMockRegistry, triggered func()) {
				p.hosts[0].Authorizer = &mockAuthorizer{
					authorize: func(ctx context.Context, r *http.Request) error {
						// When trying to authorize the request to mount, return an error
						// to force a fallback
						if isCrossRepoMount(r) {
							triggered()
							return ErrInvalidAuthorization
						}
						return nil
					},
				}
			},
		},
		{
			name: "Authorizer.AddResponses error",
			setup: func(t *testing.T, p *dockerPusher, reg *uploadableMockRegistry, triggered func()) {

				reg.defaultHandlerFunc = func(w http.ResponseWriter, r *http.Request) bool {
					if isCrossRepoMount(r) && r.Header.Get("Authorization") == "" {
						w.Header().Set("WWW-Authenticate", "Bearer realm=localhost")
						w.WriteHeader(http.StatusUnauthorized)
						return true
					}
					return false
				}

				var allResp []*http.Response
				var mu sync.Mutex
				p.hosts[0].Authorizer = &mockAuthorizer{
					authorize: func(ctc context.Context, r *http.Request) error {
						mu.Lock()
						defer mu.Unlock()
						hasFirstResp := slices.ContainsFunc(allResp, func(resp *http.Response) bool {
							return resp.Request.URL.String() == r.URL.String()
						})
						if hasFirstResp {
							r.Header.Add("Authorization", "Bearer test")
						}
						return nil
					},
					addResponses: func(ctx context.Context, resp []*http.Response) error {
						mu.Lock()
						defer mu.Unlock()
						// When trying to add a response to the request to mount, return an error
						// to force a fallback
						allResp = append(allResp, resp...)
						last := resp[len(resp)-1]
						if isCrossRepoMount(last.Request) {
							triggered()
							return ErrInvalidAuthorization
						}
						return nil
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			//t.Parallel()

			p, reg, _, done := samplePusher(t)
			defer done()

			var triggered atomic.Bool

			p.object = "layer@sha256:9f7d2a1b5c8d6eeadfbb8e786ce21958a1b6cc0b7c263c9d3e4eaf6f24c3a1bd"

			reg.uploadable = true
			tc.setup(t, &p, reg, func() { triggered.Store(true) })

			ct := []byte("layer-bytes")

			desc := ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    digest.FromBytes(ct),
				Size:      int64(len(ct)),
				Annotations: map[string]string{
					distributionSourceLabelKey(samplePusherHostname): samplePusherHostname + "/anotherrepository:latest",
				},
			}

			w, err := p.push(context.Background(), desc, remotes.MakeRefKey(context.Background(), desc), false)
			require.NoError(t, err)

			_, err = w.Write(ct)
			assert.NoError(t, err)
			err = w.Commit(context.Background(), desc.Size, desc.Digest)
			assert.NoError(t, err)

			assert.True(t, triggered.Load(), "error return was not triggered")
		})
	}
}

type mockAuthorizer struct {
	authorize    func(ctx context.Context, r *http.Request) error
	addResponses func(ctx context.Context, resp []*http.Response) error
}

func (a *mockAuthorizer) Authorize(ctx context.Context, r *http.Request) error {
	if a.authorize == nil {
		return nil
	}
	return a.authorize(ctx, r)
}

func (a *mockAuthorizer) AddResponses(ctx context.Context, resp []*http.Response) error {
	if a.addResponses == nil {
		return nil
	}
	return a.addResponses(ctx, resp)
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
	if err := content.Copy(ctx, cw, bytes.NewReader(layerContent), int64(len(layerContent)), desc.Digest); err != nil {
		return err
	}

	cContent, err := json.Marshal(ocispec.Image{})
	if err != nil {
		return err
	}
	cdesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(cContent),
		Size:      int64(len(cContent)),
	}
	cwc, err := p.Writer(ctx, content.WithRef("test-1-c"), content.WithDescriptor(cdesc))
	if err != nil {
		return err
	}
	defer cwc.Close()
	if _, err := cwc.Write(cContent); err != nil {
		return err
	}
	if err := content.Copy(ctx, cwc, bytes.NewReader(cContent), int64(len(cContent)), cdesc.Digest); err != nil {
		return err
	}

	m := ocispec.Manifest{
		Versioned: ocispecv.Versioned{SchemaVersion: 1},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    cdesc,
		Layers:    []ocispec.Descriptor{desc},
	}
	mContent, err := json.Marshal(m)
	if err != nil {
		return err
	}
	mdesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(mContent),
		Size:      int64(len(mContent)),
	}
	cwm, err := p.Writer(ctx, content.WithRef("test-1-m"), content.WithDescriptor(mdesc))
	if err != nil {
		return err
	}
	defer cwm.Close()
	if err := content.Copy(ctx, cwm, bytes.NewReader(mContent), int64(len(mContent)), mdesc.Digest); err != nil {
		return err
	}
	return nil
}

const (
	samplePusherHostname = "example.com"
	samplePusherLocator  = samplePusherHostname + "/samplerepository:latest"
)

func samplePusher(t *testing.T) (dockerPusher, *uploadableMockRegistry, StatusTrackLocker, func()) {
	reg := &uploadableMockRegistry{
		availableContents: make([]string, 0),
	}
	s := httptest.NewServer(reg)
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewInMemoryTracker()
	return dockerPusher{
		dockerBase: &dockerBase{
			refspec: reference.Spec{
				Locator: samplePusherLocator,
			},
			repository: "samplerepository",
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
		object:  "latest",
		tracker: tracker,
	}, reg, tracker, s.Close
}

var manifestRegexp = regexp.MustCompile(`/([a-z0-9]+)/manifests/(.*)`)
var blobUploadRegexp = regexp.MustCompile(`/([a-z0-9]+)/blobs/uploads/(.*)`)

// uploadableMockRegistry provides minimal registry APIs which are enough to serve requests from dockerPusher.
type uploadableMockRegistry struct {
	availableContents  []string
	uploadable         bool
	putHandlerFunc     func(w http.ResponseWriter, r *http.Request) bool
	defaultHandlerFunc func(w http.ResponseWriter, r *http.Request) bool
	locationPrefix     string
	username           string
	secret             string
}

func (u *uploadableMockRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u.secret != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || user != u.username || pass != u.secret {
			w.Header().Add("WWW-Authenticate", "basic realm=test")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	if r.Method == http.MethodPut && u.putHandlerFunc != nil {
		// if true return the response without calling default handler
		if u.putHandlerFunc(w, r) {
			return
		}
	}
	if u.defaultHandlerFunc != nil && u.defaultHandlerFunc(w, r) {
		return
	}
	u.defaultHandler(w, r)
}

func (u *uploadableMockRegistry) defaultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if matches := blobUploadRegexp.FindStringSubmatch(r.URL.Path); len(matches) != 0 {
			if u.uploadable {
				w.Header().Set("Location", u.locationPrefix+"/upload")
			} else {
				w.Header().Set("Location", u.locationPrefix+"/cannotupload")
			}

			dgstr := digest.Canonical.Digester()

			if _, err := io.Copy(dgstr.Hash(), r.Body); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			query := r.URL.Query()
			if query.Has("mount") && query.Get("from") == "always-mount" {
				w.Header().Set("Docker-Content-Digest", dgstr.Digest().String())
				w.WriteHeader(http.StatusCreated)
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

	p, reg, tracker, done := samplePusher(t)
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
		annotations       map[string]string
	}
	tests := []struct {
		name             string
		dp               dockerPusher
		dockerBaseObject string
		args             args
		checkerFunc      func(writer *pushWriter) bool
		wantErr          error
		wantStatus       *PushStatus
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
			wantStatus: &PushStatus{
				Exists:      true,
				MountedFrom: "",
			},
		},
		{
			name: "success cross-repo mount a blob layer",
			dp:   p,
			// Not needed to set the base object as it is used to generate path only in case of manifests
			// dockerBaseObject:
			args: args{
				content:           layerContent,
				mediatype:         ocispec.MediaTypeImageLayer,
				ref:               fmt.Sprintf("layer2-%s", layerContentDigest.String()),
				unavailableOnFail: false,
				annotations: map[string]string{
					distributionSourceLabelKey("example.com"): "always-mount",
				},
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
			wantErr: fmt.Errorf("content %v on remote: %w", digest.FromBytes(layerContent), errdefs.ErrAlreadyExists),
			wantStatus: &PushStatus{
				MountedFrom: "example.com/always-mount",
				Exists:      false,
			},
		},
		{
			name: "failed to cross-repo mount a blob layer",
			dp:   p,
			// Not needed to set the base object as it is used to generate path only in case of manifests
			// dockerBaseObject:
			args: args{
				content:           layerContent,
				mediatype:         ocispec.MediaTypeImageLayer,
				ref:               fmt.Sprintf("layer3-%s", layerContentDigest.String()),
				unavailableOnFail: false,
				annotations: map[string]string{
					distributionSourceLabelKey("example.com"): "never-mount",
				},
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
			wantStatus: &PushStatus{
				MountedFrom: "",
				Exists:      false,
			},
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
			wantStatus: &PushStatus{
				MountedFrom: "",
				Exists:      false,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desc := ocispec.Descriptor{
				MediaType:   test.args.mediatype,
				Digest:      digest.FromBytes(test.args.content),
				Size:        int64(len(test.args.content)),
				Annotations: test.args.annotations,
			}

			test.dp.object = test.dockerBaseObject

			got, err := test.dp.push(context.Background(), desc, test.args.ref, test.args.unavailableOnFail)

			assert.Equal(t, test.wantErr, err)

			if test.wantStatus != nil {
				status, err := tracker.GetStatus(test.args.ref)
				assert.NoError(t, err)
				assert.Equal(t, *test.wantStatus, status.PushStatus)
			}

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

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

package images

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/remotes"
	profilestore "github.com/containerd/containerd/v2/internal/cri/store/securityprofile"
	"github.com/containerd/containerd/v2/pkg/imageverifier"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	crierrors "k8s.io/cri-api/pkg/errors"
)

func profileRegistry(t *testing.T, data []byte, change func(*imagespec.Manifest)) (string, *atomic.Int32) {
	t.Helper()
	layer := imagespec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(data), Size: int64(len(data))}
	manifest := imagespec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: imagespec.MediaTypeImageManifest, Config: imagespec.Descriptor{MediaType: seccompConfigMediaType, Digest: digest.FromString("{}"), Size: 2}, Layers: []imagespec.Descriptor{layer}}
	if change != nil {
		change(&manifest)
	}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	md := digest.FromBytes(raw)
	hits := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if user, password, ok := r.BasicAuth(); !ok || user != "test" || password != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="profiles"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", imagespec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", md.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
			if r.Method != "HEAD" {
				_, _ = w.Write(raw)
			}
		case strings.HasSuffix(r.URL.Path, digest.FromString("{}").String()):
			w.Header().Set("Content-Length", "2")
			_, _ = w.Write([]byte("{}"))
		case strings.Contains(r.URL.Path, "/blobs/"):
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://") + "/profiles@" + md.String(), hits
}

func profileService(t *testing.T) *GRPCCRIImageService {
	t.Helper()
	s, g := newTestCRIService()
	var err error
	s.securityProfiles, err = profilestore.NewStore(t.TempDir())
	require.NoError(t, err)
	return g
}

func TestSecurityProfilePullLifecycle(t *testing.T) {
	ctx := context.Background()
	ref, hits := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), nil)
	g := profileService(t)
	req := &runtime.PullSecurityProfileRequest{Image: &runtime.ImageSpec{Image: ref}, Auth: &runtime.AuthConfig{Username: "test", Password: "secret"}, ProfileKind: runtime.SecurityProfileKind_Seccomp}
	response, err := g.PullSecurityProfile(ctx, req)
	require.NoError(t, err)
	require.False(t, response.Cached)
	before := hits.Load()
	req.Auth = nil // cached hits neither authenticate nor contact the registry
	response, err = g.PullSecurityProfile(ctx, req)
	require.NoError(t, err)
	require.True(t, response.Cached)
	require.Equal(t, before, hits.Load())
	list, err := g.ListSecurityProfiles(ctx, &runtime.ListSecurityProfilesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Profiles, 1)
	require.Equal(t, []string{ref}, list.Profiles[0].Refs)
	images, err := g.ListImages(ctx, &runtime.ListImagesRequest{})
	require.NoError(t, err)
	require.Empty(t, images.Images)
	remove := &runtime.RemoveSecurityProfileRequest{Digest: list.Profiles[0].Digest}
	_, err = g.RemoveSecurityProfile(ctx, remove)
	require.NoError(t, err)
	_, err = g.RemoveSecurityProfile(ctx, remove)
	require.NoError(t, err)
	list, err = g.ListSecurityProfiles(ctx, &runtime.ListSecurityProfilesRequest{})
	require.NoError(t, err)
	require.Empty(t, list.Profiles)
}

func TestSecurityProfileConcurrentPullIsDeduplicated(t *testing.T) {
	ref, _ := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), nil)
	g := profileService(t)
	request := &runtime.PullSecurityProfileRequest{
		Image:       &runtime.ImageSpec{Image: ref},
		Auth:        &runtime.AuthConfig{Username: "test", Password: "secret"},
		ProfileKind: runtime.SecurityProfileKind_Seccomp,
	}
	type result struct {
		cached bool
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := g.PullSecurityProfile(context.Background(), request)
			if response != nil {
				results <- result{cached: response.Cached, err: err}
				return
			}
			results <- result{err: err}
		}()
	}
	wg.Wait()
	close(results)
	cachedCount := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.cached {
			cachedCount++
		}
	}
	require.Equal(t, 1, cachedCount)
}

func TestSecurityProfilePullRejects(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		change     func(*imagespec.Manifest)
	}{
		{name: "wrong kind", data: `{"defaultAction":"SCMP_ACT_ALLOW"}`, change: func(m *imagespec.Manifest) {
			m.Config.MediaType = "application/vnd.cncf.apparmor-profile.config.v1+json"
		}},
		{name: "no layers", data: `{}`, change: func(m *imagespec.Manifest) { m.Layers = nil }},
		{name: "two layers", data: `{}`, change: func(m *imagespec.Manifest) { m.Layers = append(m.Layers, m.Layers[0]) }},
		{name: "oversized descriptor", data: `{}`, change: func(m *imagespec.Manifest) { m.Layers[0].Size = profilestore.MaxSize + 1 }},
		{name: "invalid JSON", data: `not JSON`},
		{name: "notify", data: `{"defaultAction":"SCMP_ACT_NOTIFY"}`},
		{name: "unknown member", data: `{"defaultAction":"SCMP_ACT_ALLOW","unknown":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, _ := profileRegistry(t, []byte(tc.data), tc.change)
			g := profileService(t)
			_, err := g.PullSecurityProfile(context.Background(), &runtime.PullSecurityProfileRequest{Image: &runtime.ImageSpec{Image: ref}, Auth: &runtime.AuthConfig{Username: "test", Password: "secret"}, ProfileKind: runtime.SecurityProfileKind_Seccomp})
			require.ErrorContains(t, err, crierrors.ErrSecurityProfileInvalid.Error())
			list, err := g.ListSecurityProfiles(context.Background(), &runtime.ListSecurityProfilesRequest{})
			require.NoError(t, err)
			require.Empty(t, list.Profiles)
		})
	}
	g := profileService(t)
	for _, kind := range []runtime.SecurityProfileKind{runtime.SecurityProfileKind_SecurityProfileKindUnspecified, runtime.SecurityProfileKind(2)} {
		_, err := g.PullSecurityProfile(context.Background(), &runtime.PullSecurityProfileRequest{ProfileKind: kind})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

type rejectingProfileVerifier struct{}

func (rejectingProfileVerifier) VerifyImage(context.Context, string, imagespec.Descriptor) (*imageverifier.Judgement, error) {
	return &imageverifier.Judgement{OK: false}, nil
}

func TestSecurityProfileSignatureRejected(t *testing.T) {
	ref, _ := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), nil)
	g := profileService(t)
	g.profileVerifiers = map[string]imageverifier.ImageVerifier{"policy": rejectingProfileVerifier{}}
	_, err := g.PullSecurityProfile(context.Background(), &runtime.PullSecurityProfileRequest{Image: &runtime.ImageSpec{Image: ref}, Auth: &runtime.AuthConfig{Username: "test", Password: "secret"}, ProfileKind: runtime.SecurityProfileKind_Seccomp})
	require.ErrorContains(t, err, crierrors.ErrSignatureValidationFailed.Error())
}

type countingRejectingProfileVerifier struct {
	calls atomic.Int32
}

func (v *countingRejectingProfileVerifier) VerifyImage(context.Context, string, imagespec.Descriptor) (*imageverifier.Judgement, error) {
	v.calls.Add(1)
	return &imageverifier.Judgement{OK: false}, nil
}

func TestSecurityProfileCachedDigestAliasMustBeVerified(t *testing.T) {
	ref, _ := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), nil)
	g := profileService(t)
	request := &runtime.PullSecurityProfileRequest{
		Image:       &runtime.ImageSpec{Image: ref},
		Auth:        &runtime.AuthConfig{Username: "test", Password: "secret"},
		ProfileKind: runtime.SecurityProfileKind_Seccomp,
	}
	_, err := g.PullSecurityProfile(context.Background(), request)
	require.NoError(t, err)

	verifier := &countingRejectingProfileVerifier{}
	g.profileVerifiers = map[string]imageverifier.ImageVerifier{"reject": verifier}
	parts := strings.SplitN(ref, "@", 2)
	alias := strings.TrimSuffix(parts[0], "/profiles") + "/other@" + parts[1]
	request.Image = &runtime.ImageSpec{Image: alias}
	_, err = g.PullSecurityProfile(context.Background(), request)
	require.ErrorContains(t, err, crierrors.ErrSignatureValidationFailed.Error())
	require.EqualValues(t, 1, verifier.calls.Load())
	_, err = g.LoadSecurityProfile(alias)
	require.True(t, errdefs.IsNotFound(err))
}

func TestSecurityProfilePullProgressTimeoutAndLimiterRelease(t *testing.T) {
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	layer := imagespec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(data), Size: int64(len(data))}
	manifest := imagespec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: imagespec.MediaTypeImageManifest,
		Config:    imagespec.Descriptor{MediaType: seccompConfigMediaType, Digest: digest.FromString("{}"), Size: 2},
		Layers:    []imagespec.Descriptor{layer},
	}
	manifestData, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDigest := digest.FromBytes(manifestData)
	stalled := make(chan struct{})
	var stalledOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "test" || password != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="profiles"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", imagespec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(manifestData)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(manifestData)
			}
		case strings.HasSuffix(r.URL.Path, digest.FromString("{}").String()):
			w.Header().Set("Content-Length", "2")
			_, _ = w.Write([]byte("{}"))
		case strings.Contains(r.URL.Path, "/blobs/"):
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			stalledOnce.Do(func() { close(stalled) })
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	g := profileService(t)
	g.config.ImagePullProgressTimeout = "50ms"
	g.downloadLimiter = semaphore.NewWeighted(1)
	ref := strings.TrimPrefix(server.URL, "http://") + "/profile@" + manifestDigest.String()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err = g.PullSecurityProfile(ctx, &runtime.PullSecurityProfileRequest{
		Image:       &runtime.ImageSpec{Image: ref},
		Auth:        &runtime.AuthConfig{Username: "test", Password: "secret"},
		ProfileKind: runtime.SecurityProfileKind_Seccomp,
	})
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
	select {
	case <-stalled:
	case <-time.After(time.Second):
		t.Fatal("registry request was not canceled after the progress timeout")
	}
	limiterCtx, limiterCancel := context.WithTimeout(context.Background(), time.Second)
	defer limiterCancel()
	require.NoError(t, g.downloadLimiter.Acquire(limiterCtx, 1))
	g.downloadLimiter.Release(1)
}

func TestSecurityProfileBlobIntegrity(t *testing.T) {
	data := []byte("{}")
	fetcher := remotes.FetcherFunc(func(context.Context, imagespec.Descriptor) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	for _, desc := range []imagespec.Descriptor{
		{Digest: digest.FromString("different"), Size: int64(len(data))},
		{Digest: digest.FromBytes(data), Size: 1},
		{Digest: digest.FromBytes(data), Size: profilestore.MaxSize + 1},
	} {
		_, err := fetchProfileBlob(context.Background(), fetcher, desc, profilestore.MaxSize)
		require.ErrorContains(t, err, crierrors.ErrSecurityProfileInvalid.Error())
	}
}

func TestSecurityProfileEmptyConfig(t *testing.T) {
	ref, _ := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), func(m *imagespec.Manifest) {
		m.ArtifactType = seccompConfigMediaType
		m.Config.MediaType = imagespec.MediaTypeEmptyJSON
	})
	g := profileService(t)
	_, err := g.PullSecurityProfile(context.Background(), &runtime.PullSecurityProfileRequest{Image: &runtime.ImageSpec{Image: ref}, Auth: &runtime.AuthConfig{Username: "test", Password: "secret"}, ProfileKind: runtime.SecurityProfileKind_Seccomp})
	require.NoError(t, err)
}

func TestSecurityProfileIgnoresDescriptorURLs(t *testing.T) {
	var externalRequests atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		externalRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(external.Close)
	ref, _ := profileRegistry(t, []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`), func(manifest *imagespec.Manifest) {
		manifest.Config.URLs = []string{external.URL + "/config"}
		manifest.Layers[0].URLs = []string{external.URL + "/layer"}
	})
	g := profileService(t)
	_, err := g.PullSecurityProfile(context.Background(), &runtime.PullSecurityProfileRequest{
		Image:       &runtime.ImageSpec{Image: ref},
		Auth:        &runtime.AuthConfig{Username: "test", Password: "secret"},
		ProfileKind: runtime.SecurityProfileKind_Seccomp,
	})
	require.NoError(t, err)
	require.Zero(t, externalRequests.Load())
}

func TestSecurityProfileMetricsUseStoredUsage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	store, err := profilestore.NewStore(root)
	require.NoError(t, err)
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW"}`)
	require.NoError(t, store.Put("registry.example/profile@"+digest.FromString("manifest").String(), data))
	previous := securityProfileMetricsStore.Swap(store)
	t.Cleanup(func() { securityProfileMetricsStore.Store(previous) })
	// Scrapes must use the snapshot even if the on-disk directory is unavailable.
	require.NoError(t, os.Rename(root, root+".moved"))
	require.Equal(t, float64(1), securityProfileUsage(false))
	require.Equal(t, float64(len(data)), securityProfileUsage(true))
}

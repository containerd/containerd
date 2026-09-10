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

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func emptyOCITestImage() (map[digest.Digest][]byte, ocispec.Descriptor) {
	blobs := make(map[digest.Digest][]byte)

	layerBytes := make([]byte, 1024)
	layerDigest := digest.FromBytes(layerBytes)
	blobs[layerDigest] = layerBytes

	configJSON := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["` + layerDigest.String() + `"]}}`)
	configDigest := digest.FromBytes(configJSON)
	blobs[configDigest] = configJSON

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    configDigest,
			Size:      int64(len(configJSON)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layerBytes)),
			},
		},
	}
	manifestJSON, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestJSON)
	blobs[manifestDigest] = manifestJSON

	target := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    manifestDigest,
		Size:      int64(len(manifestJSON)),
	}

	return blobs, target
}

func TestImagePullWith429RetryAfter(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	blobs, target := emptyOCITestImage()

	var attempts atomic.Int32
	interceptor := func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/manifests/") {
			if attempts.Add(1) <= 2 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"rate limit exceeded"}]}`))
				return true
			}
		}
		return false
	}

	ref := testutil.ServeImageWithInterceptor(t, "retryafter-429", "latest", target, blobs, interceptor)
	t.Cleanup(func() {
		client.ImageService().Delete(ctx, ref)
	})

	start := time.Now()
	image, err := client.Pull(ctx, ref, WithPlatformMatcher(platforms.Default()))
	require.NoError(t, err)
	require.NotNil(t, image)

	// Since two 429s each requested 1s Retry-After, pull must have taken >= 2 seconds
	assert.GreaterOrEqual(t, time.Since(start), 2*time.Second)
	assert.GreaterOrEqual(t, attempts.Load(), int32(3))
}

func TestImagePullWith429ExponentialBackoff(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	blobs, target := emptyOCITestImage()

	var attempts atomic.Int32
	interceptor := func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/manifests/") {
			// Return 429 without Retry-After twice to test exponential backoff
			// Attempt 1 backoff: ~500ms
			// Attempt 2 backoff: ~1000ms
			// Total backoff delay: ~1500ms
			if attempts.Add(1) <= 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`rate limit reached`))
				return true
			}
		}
		return false
	}

	ref := testutil.ServeImageWithInterceptor(t, "backoff-429", "latest", target, blobs, interceptor)
	t.Cleanup(func() {
		client.ImageService().Delete(ctx, ref)
	})

	start := time.Now()
	image, err := client.Pull(ctx, ref, WithPlatformMatcher(platforms.Default()))
	require.NoError(t, err)
	require.NotNil(t, image)

	// Exponential backoff with 500ms base (down to 400ms with jitter) doubling to 1000ms (down to 800ms with jitter) means >= 1200ms elapsed
	assert.GreaterOrEqual(t, time.Since(start), 1200*time.Millisecond)
	assert.GreaterOrEqual(t, attempts.Load(), int32(3))
}

func TestImagePullWith429ContextTimeout(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := testContext(t)
	t.Cleanup(cancel)
	ctx, timeoutCancel := context.WithTimeout(ctx, 1*time.Second)
	t.Cleanup(timeoutCancel)

	blobs, target := emptyOCITestImage()

	var attempts atomic.Int32
	interceptor := func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/manifests/") {
			attempts.Add(1)
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}

	ref := testutil.ServeImageWithInterceptor(t, "timeout-429", "latest", target, blobs, interceptor)
	t.Cleanup(func() {
		ctxClean, cancelClean := testContext(t)
		defer cancelClean()
		client.ImageService().Delete(ctxClean, ref)
	})

	start := time.Now()
	_, err = client.Pull(ctx, ref, WithPlatformMatcher(platforms.Default()))
	require.Error(t, err)

	// Since Retry-After is 60s but ctx timeout is 1s, pull should abort after ~1s
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 1*time.Second)
	assert.Less(t, elapsed, 10*time.Second)
	assert.Equal(t, int32(1), attempts.Load())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestImagePullWith429ExhaustedRetries(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	blobs, target := emptyOCITestImage()

	var attempts atomic.Int32
	interceptor := func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/manifests/") {
			attempts.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}

	ref := testutil.ServeImageWithInterceptor(t, "exhausted-429", "latest", target, blobs, interceptor)
	t.Cleanup(func() {
		client.ImageService().Delete(ctx, ref)
	})

	_, err = client.Pull(ctx, ref, WithPlatformMatcher(platforms.Default()))
	require.Error(t, err)

	// All 5 attempts exhausted
	assert.Equal(t, int32(5), attempts.Load())
	assert.Contains(t, err.Error(), "429 Too Many Requests")
	assert.Contains(t, err.Error(), "(Retry-After: 0)")
}

func TestImagePullWith429ExcessiveRetryAfter(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx, cancel := testContext(t)
	t.Cleanup(cancel)

	blobs, target := emptyOCITestImage()

	var attempts atomic.Int32
	interceptor := func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/manifests/") {
			attempts.Add(1)
			w.Header().Set("Retry-After", "300")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}

	ref := testutil.ServeImageWithInterceptor(t, "excessive-429", "latest", target, blobs, interceptor)
	t.Cleanup(func() {
		client.ImageService().Delete(ctx, ref)
	})

	start := time.Now()
	_, err = client.Pull(ctx, ref, WithPlatformMatcher(platforms.Default()))
	require.Error(t, err)

	// Fails immediately on first attempt without retrying or sleeping
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, int32(1), attempts.Load())
	assert.Contains(t, err.Error(), "429 Too Many Requests")
	assert.Contains(t, err.Error(), "(Retry-After: 300)")
}

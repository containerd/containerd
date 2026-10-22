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
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes/docker"
)

func TestFetchIgnoreDescriptorURLs(t *testing.T) {
	ctx := t.Context()

	layerBytes := []byte("foreign layer payload")
	layerDigest := digest.FromBytes(layerBytes)

	configBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["` + layerDigest.String() + `"]}}`)
	configDigest := digest.FromBytes(configBytes)

	var externalHits atomic.Int32
	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalHits.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(layerBytes)))
		_, _ = w.Write(layerBytes)
	}))
	t.Cleanup(externalServer.Close)

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    configDigest,
			Size:      int64(len(configBytes)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layerBytes)),
				URLs:      []string{externalServer.URL + "/layer"},
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDigest := digest.FromBytes(manifestBytes)

	var registryLayerHits atomic.Int32
	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/image/manifests/latest", "/v2/test/image/manifests/" + manifestDigest.String():
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(manifestBytes)))
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(manifestBytes)
		case "/v2/test/image/blobs/" + configDigest.String():
			w.Header().Set("Content-Length", strconv.Itoa(len(configBytes)))
			_, _ = w.Write(configBytes)
		case "/v2/test/image/blobs/" + layerDigest.String():
			registryLayerHits.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(layerBytes)))
			_, _ = w.Write(layerBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(registryServer.Close)

	ref := strings.TrimPrefix(registryServer.URL, "http://") + "/test/image:latest"

	t.Run("default follows descriptor URLs", func(t *testing.T) {
		externalHits.Store(0)
		registryLayerHits.Store(0)

		c := newTestFetchClient(t)
		_, err := c.Fetch(ctx, ref,
			WithResolver(docker.NewResolver(docker.ResolverOptions{PlainHTTP: true})),
		)
		require.NoError(t, err)
		assert.EqualValues(t, 1, externalHits.Load())
		assert.EqualValues(t, 0, registryLayerHits.Load())
	})

	t.Run("WithIgnoreDescriptorURLs skips descriptor URLs", func(t *testing.T) {
		externalHits.Store(0)
		registryLayerHits.Store(0)

		c := newTestFetchClient(t)
		_, err := c.Fetch(ctx, ref,
			WithResolver(docker.NewResolver(docker.ResolverOptions{PlainHTTP: true})),
			WithIgnoreDescriptorURLs(true),
		)
		require.NoError(t, err)
		assert.EqualValues(t, 0, externalHits.Load())
		assert.EqualValues(t, 1, registryLayerHits.Load())
	})
	t.Run("Pull WithIgnoreDescriptorURLs skips descriptor URLs", func(t *testing.T) {
		externalHits.Store(0)
		registryLayerHits.Store(0)

		c := newTestFetchClient(t)
		_, err := c.Pull(ctx, ref,
			WithResolver(docker.NewResolver(docker.ResolverOptions{PlainHTTP: true})),
			WithIgnoreDescriptorURLs(true),
		)
		require.NoError(t, err)
		assert.EqualValues(t, 0, externalHits.Load())
		assert.EqualValues(t, 1, registryLayerHits.Load())
	})
}

func newTestFetchClient(t *testing.T) *Client {
	t.Helper()
	cs := imagetest.NewContentStore(t.Context(), t)
	c, err := New("", WithServices(
		WithContentStore(cs.Store),
		WithImageStore(&fakeImageStore{}),
		WithLeasesService(&fakeLeasesManager{}),
	))
	require.NoError(t, err)
	return c
}

type fakeImageStore struct {
	images.Store
}

func (f *fakeImageStore) Create(_ context.Context, image images.Image) (images.Image, error) {
	return image, nil
}

type fakeLeasesManager struct {
	leases.Manager
}

func (f *fakeLeasesManager) Create(_ context.Context, _ ...leases.Opt) (leases.Lease, error) {
	return leases.Lease{ID: "test-lease"}, nil
}

func (f *fakeLeasesManager) Delete(_ context.Context, _ leases.Lease, _ ...leases.DeleteOpt) error {
	return nil
}

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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/introspection"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
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

func TestPullLegacyConfig(t *testing.T) {
	// Build a real tar+gzip layer and serve it without an external registry.
	var tarBuffer, compressed bytes.Buffer
	tw := tar.NewWriter(&tarBuffer)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0644, Size: 5}))
	_, err := tw.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	zw := gzip.NewWriter(&compressed)
	_, err = zw.Write(tarBuffer.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	layer := compressed.Bytes()
	diffID := digest.FromBytes(tarBuffer.Bytes())

	descriptor := func(mediaType string, data []byte) ocispec.Descriptor {
		return ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
	}
	for _, configType := range []string{docker.LegacyConfigMediaType, images.MediaTypeDockerSchema2Config, ocispec.MediaTypeImageConfig} {
		t.Run(configType, func(t *testing.T) {
			manifestType, layerType := images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2LayerGzip
			if configType == ocispec.MediaTypeImageConfig {
				manifestType, layerType = ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageLayerGzip
			}
			config, err := json.Marshal(ocispec.Image{
				Platform: platforms.DefaultSpec(),
				RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{diffID}},
			})
			require.NoError(t, err)
			configDesc, layerDesc := descriptor(configType, config), descriptor(layerType, layer)
			manifest, err := json.Marshal(ocispec.Manifest{
				Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: manifestType,
				Config: configDesc, Layers: []ocispec.Descriptor{layerDesc},
			})
			require.NoError(t, err)
			manifestDesc := descriptor(manifestType, manifest)
			ref := testutil.ServeImage(t, "image", "latest", manifestDesc, map[digest.Digest][]byte{
				manifestDesc.Digest: manifest, configDesc.Digest: config, layerDesc.Digest: layer,
			})
			for _, unpack := range []bool{false, true} {
				name := "pull"
				if unpack {
					name = "pull-unpack"
				}
				t.Run(name, func(t *testing.T) {
					// Each pull starts with an empty store. The lease avoids needing a daemon.
					ctx := leases.WithLease(context.Background(), "test")
					store := imagetest.NewContentStore(ctx, t).Store
					sn, err := native.NewSnapshotter(t.TempDir())
					require.NoError(t, err)
					defer sn.Close()
					applier := &pullTestDiff{store: store}
					c, err := New("", WithServices(
						WithContentStore(store), WithImageStore(pullTestImages{}),
						WithSnapshotters(map[string]snapshots.Snapshotter{"native": sn}),
						WithDiffService(applier), WithIntrospectionService(pullTestIntrospection{}),
					))
					require.NoError(t, err)
					defer c.Close()
					opts := []RemoteOpt{WithResolver(docker.NewResolver(docker.ResolverOptions{})), WithPullSnapshotter("native")}
					if unpack {
						opts = append(opts, WithPullUnpack)
					}
					img, err := c.Pull(ctx, ref, opts...)
					require.NoError(t, err)
					got, err := content.ReadBlob(ctx, store, layerDesc)
					require.NoError(t, err)
					assert.Equal(t, layer, got)
					cfg, err := img.Config(ctx)
					require.NoError(t, err)
					wantConfigType := configType
					if configType == docker.LegacyConfigMediaType {
						wantConfigType = images.MediaTypeDockerSchema2Config
						assert.NotEqual(t, manifestDesc.Digest, img.Target().Digest)
					}
					assert.Equal(t, wantConfigType, cfg.MediaType)
					if unpack {
						assert.Equal(t, 1, applier.applied)
						info, err := sn.Stat(ctx, diffID.String())
						require.NoError(t, err)
						assert.Equal(t, snapshots.KindCommitted, info.Kind)
						configInfo, err := store.Info(ctx, cfg.Digest)
						require.NoError(t, err)
						assert.Equal(t, diffID.String(), configInfo.Labels["containerd.io/gc.ref.snapshot.native"])
					} else {
						assert.Zero(t, applier.applied)
					}
				})
			}
		})
	}
}

type pullTestImages struct{ images.Store }

func (pullTestImages) Create(_ context.Context, img images.Image) (images.Image, error) {
	return img, nil
}

type pullTestIntrospection struct{ introspection.Service }

func (pullTestIntrospection) Plugins(context.Context, ...string) (*introspectionapi.PluginsResponse, error) {
	return &introspectionapi.PluginsResponse{Plugins: []*introspectionapi.Plugin{{ID: "native"}}}, nil
}

// Read and decompress the fetched blob without requiring privileged mounts.
type pullTestDiff struct {
	DiffService
	store   content.Store
	applied int
}

func (d *pullTestDiff) Apply(ctx context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	data, err := content.ReadBlob(ctx, d.store, desc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer zr.Close()
	data, err = io.ReadAll(zr)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	d.applied++
	return ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromBytes(data), Size: int64(len(data))}, nil
}

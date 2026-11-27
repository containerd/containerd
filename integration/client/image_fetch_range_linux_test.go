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
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes/docker"
)

func TestImageFetchParallelChunkRanges(t *testing.T) {
	const (
		chunkSize = 256 * 1024
		repo      = "test/fetch-ranges"
	)

	layer := make([]byte, 10*chunkSize+12345)
	_, err := rand.Read(layer)
	require.NoError(t, err)
	config, err := json.Marshal(ocispec.Image{
		Platform: platforms.DefaultSpec(),
		RootFS:   ocispec.RootFS{Type: "layers"},
	})
	require.NoError(t, err)
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(layer),
		Size:      int64(len(layer)),
	}
	manifest, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes(config),
			Size:      int64(len(config)),
		},
		Layers: []ocispec.Descriptor{layerDesc},
	})
	require.NoError(t, err)
	blobs := map[digest.Digest][]byte{
		layerDesc.Digest:         layer,
		digest.FromBytes(config): config,
	}

	var (
		mu     sync.Mutex
		ranges = map[digest.Digest][]string{}
	)
	manifestDigest := digest.FromBytes(manifest)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ref, ok := strings.CutPrefix(r.URL.Path, "/v2/"+repo+"/manifests/"); ok && (ref == "latest" || ref == manifestDigest.String()) {
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(manifest))
			return
		}
		dgst, ok := strings.CutPrefix(r.URL.Path, "/v2/"+repo+"/blobs/")
		b, found := blobs[digest.Digest(dgst)]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			mu.Lock()
			ranges[digest.Digest(dgst)] = append(ranges[digest.Digest(dgst)], r.Header.Get("Range"))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
	}))
	defer srv.Close()

	resolver := docker.NewResolver(docker.ResolverOptions{
		Hosts: docker.ConfigureDefaultRegistries(docker.WithPlainHTTP(docker.MatchLocalhost)),
	})

	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	img, err := client.Fetch(ctx, strings.TrimPrefix(srv.URL, "http://")+"/"+repo+":latest",
		WithResolver(resolver),
		WithMaxConcurrentDownloads(3),
		WithConcurrentLayerFetchBuffer(chunkSize),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, client.ImageService().Delete(ctx, img.Name, images.SynchronousDelete()))
	}()

	mu.Lock()
	defer mu.Unlock()
	assert.Greater(t, len(ranges[layerDesc.Digest]), 1, "layer was not fetched in chunks")
	for dgst, hs := range ranges {
		size := int64(len(blobs[dgst]))
		var requested int64
		for _, h := range hs {
			n, err := requestedBytes(h, size)
			require.NoError(t, err)
			requested += n
		}
		assert.Equal(t, size, requested, "bytes requested for %s with ranges %v", dgst, hs)
	}
}

func requestedBytes(h string, size int64) (int64, error) {
	if h == "" {
		return size, nil
	}
	rng, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return 0, fmt.Errorf("unexpected range %q", h)
	}
	first, last, ok := strings.Cut(rng, "-")
	if !ok {
		return 0, fmt.Errorf("unexpected range %q", h)
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected range %q: %w", h, err)
	}
	end := size - 1
	if last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil {
			return 0, fmt.Errorf("unexpected range %q: %w", h, err)
		}
	}
	return min(end, size-1) - start + 1, nil
}

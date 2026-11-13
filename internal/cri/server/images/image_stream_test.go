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
	"context"
	"fmt"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
)

// fakeStreamServer is a mock grpc.ServerStreamingServer that captures sent messages.
type fakeStreamServer[Res any] struct {
	ctx  context.Context
	sent []*Res
}

func TestStreamImagesBatchSize(t *testing.T) {
	service, _ := newTestCRIService()
	var imagesInStore []imagestore.Image
	var expectedIDs []string
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("sha256:%064x", i)
		expectedIDs = append(expectedIDs, id)
		imagesInStore = append(imagesInStore, imagestore.Image{
			ID:         id,
			ChainID:    fmt.Sprintf("chain-%d", i),
			References: []string{fmt.Sprintf("example.com/image-%d:latest", i)},
		})
	}
	var err error
	service.imageStore, err = imagestore.NewFakeStore(imagesInStore)
	require.NoError(t, err)

	for _, test := range []struct {
		name       string
		config     criconfig.ListStreamingConfig
		batchSizes []int
	}{
		{name: "default", config: criconfig.DefaultServerConfig().ListStreaming, batchSizes: []int{5}},
		{name: "override", config: criconfig.ListStreamingConfig{BatchSize: 2}, batchSizes: []int{2, 2, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := service.GRPCService(test.config)
			stream := newFakeStreamServer[runtime.StreamImagesResponse](context.Background())
			require.NoError(t, c.StreamImages(&runtime.StreamImagesRequest{}, stream))
			var batchSizes []int
			var ids []string
			for _, response := range stream.sent {
				batchSizes = append(batchSizes, len(response.Images))
				for _, image := range response.Images {
					ids = append(ids, image.Id)
				}
			}
			assert.Equal(t, test.batchSizes, batchSizes)
			assert.ElementsMatch(t, expectedIDs, ids)
		})
	}
}

func newFakeStreamServer[Res any](ctx context.Context) *fakeStreamServer[Res] {
	return &fakeStreamServer[Res]{ctx: ctx}
}

func (f *fakeStreamServer[Res]) Send(msg *Res) error {
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeStreamServer[Res]) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStreamServer[Res]) SendHeader(metadata.MD) error { return nil }
func (f *fakeStreamServer[Res]) SetTrailer(metadata.MD)       {}
func (f *fakeStreamServer[Res]) Context() context.Context     { return f.ctx }
func (f *fakeStreamServer[Res]) SendMsg(any) error            { return nil }
func (f *fakeStreamServer[Res]) RecvMsg(any) error            { return nil }

func TestStreamImages(t *testing.T) {
	_, c := newTestCRIService()
	imagesInStore := []imagestore.Image{
		{
			ID:      "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID: "test-chainid-1",
			References: []string{
				"gcr.io/library/busybox:latest",
				"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			},
			Size: 1000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "root",
				},
			},
		},
		{
			ID:      "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID: "test-chainid-2",
			References: []string{
				"gcr.io/library/alpine:latest",
				"gcr.io/library/alpine@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			},
			Size: 2000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "1234:1234",
				},
			},
		},
		{
			ID:      "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID: "test-chainid-3",
			References: []string{
				"gcr.io/library/ubuntu:latest",
				"gcr.io/library/ubuntu@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			},
			Size: 3000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "nobody",
				},
			},
		},
	}
	expect := []*runtime.Image{
		{
			Id:          "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/busybox:latest"},
			RepoDigests: []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size:        uint64(1000),
			Username:    "root",
		},
		{
			Id:          "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/alpine:latest"},
			RepoDigests: []string{"gcr.io/library/alpine@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size:        uint64(2000),
			Uid:         &runtime.Int64Value{Value: 1234},
		},
		{
			Id:          "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/ubuntu:latest"},
			RepoDigests: []string{"gcr.io/library/ubuntu@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size:        uint64(3000),
			Username:    "nobody",
		},
	}

	var err error
	c.imageStore, err = imagestore.NewFakeStore(imagesInStore)
	assert.NoError(t, err)

	stream := newFakeStreamServer[runtime.StreamImagesResponse](context.Background())
	err = c.StreamImages(&runtime.StreamImagesRequest{}, stream)
	assert.NoError(t, err)
	require.NotEmpty(t, stream.sent)

	var images []*runtime.Image
	for _, resp := range stream.sent {
		images = append(images, resp.Images...)
	}
	assert.Len(t, images, len(expect))
	for _, img := range expect {
		assert.Contains(t, images, img)
	}
}

func TestStreamImagesEmpty(t *testing.T) {
	_, c := newTestCRIService()

	stream := newFakeStreamServer[runtime.StreamImagesResponse](context.Background())
	err := c.StreamImages(&runtime.StreamImagesRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}

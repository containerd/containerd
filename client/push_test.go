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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/transfer"
	transferlocal "github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/pkg/labels"
)

func TestPushDistributionSourceLabels(t *testing.T) {
	const (
		targetRef = "target.example:5000/destination/image:latest"
		targetKey = labels.LabelDistributionSource + ".target.example"
		oldRepo   = "existing/image"
		newRepos  = "destination/image,existing/image"
	)
	for _, mode := range []string{"client", "transfer", "transfer without reference"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				index      bool
				reject     string
				platform   bool
				skip       bool
				remoteOnly bool
				labelError bool
			}{
				{name: "manifest"},
				{name: "index", index: true},
				{name: "manifest rejected", index: true, reject: ocispec.MediaTypeImageManifest},
				{name: "index rejected", index: true, reject: ocispec.MediaTypeImageIndex},
				{name: "platform selection", index: true, platform: true},
				{name: "skipped layer", skip: true},
				{name: "layer only in registry", remoteOnly: true},
				{name: "label update rejected", labelError: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if tc.skip && mode != "client" {
						t.Skip("custom push handlers are a client option")
					}
					ctx := t.Context()
					source := imagetest.NewContentStore(ctx, t)
					makeManifest := func(arch string) imagetest.Content {
						return imagetest.AddPlatform(source.Manifest(
							source.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
								Platform: ocispec.Platform{OS: "linux", Architecture: arch},
							}),
							source.Blob(ocispec.MediaTypeImageLayer, []byte(arch)),
						), ocispec.Platform{OS: "linux", Architecture: arch})
					}
					manifest := makeManifest("amd64")
					root := manifest
					if tc.index {
						root = source.Index(manifest, makeManifest("arm64"))
					}

					var descriptors []ocispec.Descriptor
					beforeLabels := map[digest.Digest]string{}
					source.Walk(root, func(ctx context.Context, c *imagetest.Content, store content.Store) error {
						descriptors = append(descriptors, c.Descriptor)
						beforeLabels[c.Descriptor.Digest] = oldRepo
						_, err := store.Update(ctx, content.Info{
							Digest: c.Descriptor.Digest,
							Labels: map[string]string{
								targetKey: oldRepo,
								labels.LabelDistributionSource + ".source.example": "original/image",
								"test.keep": "unchanged",
							},
						}, "labels")
						return err
					})

					remote := imagetest.NewContentStore(ctx, t)
					var missing digest.Digest
					if tc.remoteOnly {
						layer := remote.Blob(ocispec.MediaTypeImageLayer, []byte("amd64"))
						missing = manifest.Children[1].Descriptor.Digest
						require.Equal(t, missing, layer.Descriptor.Digest)
						require.NoError(t, source.Delete(ctx, missing))
					}
					rejected := errors.New("registry rejected image")
					pusher := &distributionTestPusher{
						store: remote.Store,
						seen:  map[digest.Digest]bool{},
						before: func(desc ocispec.Descriptor) error {
							for _, d := range descriptors {
								info, err := source.Info(ctx, d.Digest)
								if d.Digest == missing && errdefs.IsNotFound(err) {
									continue
								}
								if err != nil {
									return err
								}
								if info.Labels[targetKey] != beforeLabels[d.Digest] {
									return fmt.Errorf("source label changed before the image push completed: %s", d.Digest)
								}
							}
							if desc.MediaType == tc.reject {
								return rejected
							}
							return nil
						},
					}

					var push func() error
					var completed atomic.Bool
					store := source.Store
					labelErrors := &distributionTestLabelErrorStore{Store: store}
					if tc.labelError {
						store = labelErrors
					}
					if mode == "client" {
						c, err := New("", WithServices(WithContentStore(store)))
						require.NoError(t, err)
						opts := []RemoteOpt{WithResolver(distributionTestResolver{pusher: pusher})}
						if tc.platform {
							opts = append(opts, WithPlatform("linux/amd64"))
						}
						if tc.skip {
							skipped := manifest.Children[1].Descriptor.Digest
							opts = append(opts, WithImageHandler(images.HandlerFunc(func(_ context.Context, d ocispec.Descriptor) ([]ocispec.Descriptor, error) {
								if d.Digest == skipped {
									return nil, images.ErrSkipDesc
								}
								return nil, nil
							})), WithImageHandlerWrapper(func(h images.Handler) images.Handler {
								return h
							}))
						}
						push = func() error { return c.Push(ctx, targetRef, root.Descriptor, opts...) }
					} else {
						ts := transferlocal.NewTransferService(store, nil, transferlocal.TransferConfig{})
						getter := distributionTestImage{image: images.Image{
							Name:   "source.example/original/image:latest",
							Target: root.Descriptor,
						}}
						if tc.platform {
							getter.platforms = []ocispec.Platform{{OS: "linux", Architecture: "amd64"}}
						}
						var dest transfer.ImagePusher = distributionTestImagePusher{pusher: pusher}
						if mode == "transfer" {
							dest = distributionTestNamedPusher{distributionTestImagePusher{pusher: pusher}, targetRef}
						}
						push = func() error {
							return ts.Transfer(ctx, getter, dest, transfer.WithProgress(func(p transfer.Progress) {
								if p.Event == "pushed content" {
									completed.Store(true)
								}
							}))
						}
					}

					err := push()
					if tc.reject != "" {
						require.ErrorIs(t, err, rejected)
						// Ensure the rejection happened after a layer was actually uploaded.
						_, err := remote.Info(ctx, manifest.Children[1].Descriptor.Digest)
						require.NoError(t, err)
					} else {
						require.NoError(t, err)
					}
					if mode != "client" {
						assert.Equal(t, tc.reject == "", completed.Load())
					}
					if tc.labelError && mode != "transfer without reference" {
						assert.Positive(t, labelErrors.updates.Load())
					}

					for _, d := range descriptors {
						info, err := source.Info(ctx, d.Digest)
						if d.Digest == missing {
							require.True(t, errdefs.IsNotFound(err), "missing layer %s: %v", d.Digest, err)
							continue
						}
						require.NoError(t, err)
						want := oldRepo
						if tc.reject == "" && !tc.labelError && mode != "transfer without reference" && pusher.seen[d.Digest] {
							want = newRepos
						}
						assert.Equal(t, map[string]string{
							targetKey: want,
							labels.LabelDistributionSource + ".source.example": "original/image",
							"test.keep": "unchanged",
						}, info.Labels, "labels for %s", d.Digest)
						beforeLabels[d.Digest] = want
					}
					if tc.platform {
						assert.False(t, pusher.seen[root.Children[1].Descriptor.Digest])
					}
					if tc.skip {
						assert.False(t, pusher.seen[manifest.Children[1].Descriptor.Digest])
					}
					if tc.reject == "" {
						// Existing remote blobs must also retain a single destination entry.
						require.NoError(t, push())
						for _, d := range descriptors {
							if d.Digest == missing {
								continue
							}
							info, err := source.Info(ctx, d.Digest)
							require.NoError(t, err)
							assert.Equal(t, beforeLabels[d.Digest], info.Labels[targetKey])
						}
					}
				})
			}
		})
	}
}

type distributionTestResolver struct {
	remotes.Resolver
	pusher remotes.Pusher
}

func (r distributionTestResolver) Pusher(context.Context, string) (remotes.Pusher, error) {
	return r.pusher, nil
}

type distributionTestLabelErrorStore struct {
	content.Store
	updates atomic.Int32
}

func (s *distributionTestLabelErrorStore) Update(context.Context, content.Info, ...string) (content.Info, error) {
	s.updates.Add(1)
	return content.Info{}, errdefs.ErrFailedPrecondition
}

type distributionTestPusher struct {
	store  content.Store
	before func(ocispec.Descriptor) error
	mu     sync.Mutex
	seen   map[digest.Digest]bool
}

func (p *distributionTestPusher) Push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	if err := p.before(desc); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.seen[desc.Digest] = true
	p.mu.Unlock()
	if _, err := p.store.Info(ctx, desc.Digest); err == nil {
		return nil, errdefs.ErrAlreadyExists
	} else if !errdefs.IsNotFound(err) {
		return nil, err
	}
	return content.OpenWriter(ctx, p.store, content.WithRef(desc.Digest.String()), content.WithDescriptor(desc))
}

type distributionTestImage struct {
	image     images.Image
	platforms []ocispec.Platform
}

func (i distributionTestImage) Get(context.Context, images.Store) (images.Image, error) {
	return i.image, nil
}

func (i distributionTestImage) Platforms() []ocispec.Platform {
	return i.platforms
}

type distributionTestImagePusher struct {
	pusher remotes.Pusher
}

func (p distributionTestImagePusher) Pusher(context.Context, ocispec.Descriptor) (transfer.Pusher, error) {
	return p.pusher, nil
}

type distributionTestNamedPusher struct {
	distributionTestImagePusher
	ref string
}

func (p distributionTestNamedPusher) Image() string {
	return p.ref
}

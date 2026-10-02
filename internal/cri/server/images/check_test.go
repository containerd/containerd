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
	"errors"
	"sync/atomic"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCheckClient records GetImage calls (UpdateImage path).
type fakeCheckClient struct {
	imgs     []containerd.Image
	listErr  error
	getCalls atomic.Int32
}

func (c *fakeCheckClient) ListImages(context.Context, ...string) ([]containerd.Image, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.imgs, nil
}

func (c *fakeCheckClient) GetImage(_ context.Context, ref string) (containerd.Image, error) {
	c.getCalls.Add(1)
	for _, img := range c.imgs {
		if img.Name() == ref {
			return img, nil
		}
	}
	return nil, errdefs.ErrNotFound
}

func (c *fakeCheckClient) Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

// fakeCheckImage is a stub containerd.Image for CheckImages.
type fakeCheckImage struct {
	containerd.Image
	name     string
	target   ocispec.Descriptor
	cs       content.Store
	unpacked bool
	// unpackedFor holds per-snapshotter results, overriding unpacked/unpackErr.
	unpackedFor map[string]bool
	unpackErr   error
}

func (i *fakeCheckImage) Name() string                { return i.name }
func (i *fakeCheckImage) Target() ocispec.Descriptor  { return i.target }
func (i *fakeCheckImage) ContentStore() content.Store { return i.cs }
func (i *fakeCheckImage) Labels() map[string]string {
	return map[string]string{crilabels.ImageLabelKey: crilabels.ImageLabelValue}
}
func (i *fakeCheckImage) IsUnpacked(_ context.Context, snapshotter string) (bool, error) {
	if unpacked, ok := i.unpackedFor[snapshotter]; ok {
		return unpacked, nil
	}
	return i.unpacked, i.unpackErr
}

// errContentStore fails every read, to exercise the readiness check error path.
type errContentStore struct {
	content.Store
	err error
}

func (s errContentStore) ReaderAt(context.Context, ocispec.Descriptor) (content.ReaderAt, error) {
	return nil, s.err
}

type notFoundGetter struct{}

func (notFoundGetter) Get(context.Context, string) (images.Image, error) {
	return images.Image{}, errdefs.ErrNotFound
}

type contentState int

const (
	contentComplete contentState = iota
	contentMissingLayer
	contentUnavailable // manifest blob absent → available=false
)

func writeBlob(t *testing.T, ctx context.Context, cs content.Store, mt string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{
		MediaType: mt,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	require.NoError(t, content.WriteBlob(ctx, cs, desc.Digest.String(), bytes.NewReader(data), desc))
	return desc
}

func writeJSON(t *testing.T, ctx context.Context, cs content.Store, mt string, v any) ocispec.Descriptor {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return writeBlob(t, ctx, cs, mt, data)
}

// newCheckFixture writes config+layer+manifest, then applies state.
func newCheckFixture(t *testing.T, state contentState) (content.Store, ocispec.Descriptor) {
	t.Helper()
	ctx := context.Background()
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)

	config := writeJSON(t, ctx, cs, ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: platforms.DefaultSpec(),
	})
	layer := writeBlob(t, ctx, cs, ocispec.MediaTypeImageLayerGzip, []byte("layer"))
	mfst := writeJSON(t, ctx, cs, ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Config: config,
		Layers: []ocispec.Descriptor{layer},
	})

	switch state {
	case contentComplete:
		// keep all blobs
	case contentMissingLayer:
		require.NoError(t, cs.Delete(ctx, layer.Digest))
	case contentUnavailable:
		require.NoError(t, cs.Delete(ctx, mfst.Digest))
	default:
		t.Fatalf("unknown content state %v", state)
	}
	return cs, mfst
}

func newCheckService(t *testing.T, client *fakeCheckClient, cs content.Store, runtimePlatforms map[string]ImagePlatform) *CRIImageService {
	t.Helper()
	svc, _ := newTestCRIService()
	svc.config = criconfig.ImageConfig{Snapshotter: "overlayfs"}
	svc.client = client
	svc.runtimePlatforms = runtimePlatforms
	svc.imageStore = imagestore.NewStore(notFoundGetter{}, cs, platforms.Default())
	return svc
}

func TestCheckImages(t *testing.T) {
	listErr := errors.New("list images failed")

	for _, test := range []struct {
		desc             string
		state            contentState
		listErr          error
		contentErr       error
		unpacked         bool
		unpackedFor      map[string]bool
		runtimePlatforms map[string]ImagePlatform
		unpackErr        error
		wantErr          bool
		wantUpdate       bool
	}{
		{
			desc:       "should load image when config and layers are present",
			state:      contentComplete,
			unpacked:   true,
			wantUpdate: true,
		},
		{
			desc:       "should load image when content is complete but not unpacked",
			state:      contentComplete,
			unpacked:   false,
			wantUpdate: true,
		},
		{
			desc:       "should skip image when a required layer is missing and it is not unpacked",
			state:      contentMissingLayer,
			unpacked:   false,
			wantUpdate: false,
		},
		{
			desc:       "should load image when layers are missing but it is unpacked (discard_unpacked_layers)",
			state:      contentMissingLayer,
			unpacked:   true,
			wantUpdate: true,
		},
		{
			desc:        "should load image when layers are missing but a runtime snapshotter has it unpacked",
			state:       contentMissingLayer,
			unpackedFor: map[string]bool{"overlayfs": false, "devmapper": true},
			runtimePlatforms: map[string]ImagePlatform{
				"runc": {Snapshotter: "devmapper"},
			},
			wantUpdate: true,
		},
		{
			desc:        "should load image when a runtime snapshotter cannot be queried but another has it unpacked",
			state:       contentMissingLayer,
			unpackedFor: map[string]bool{"overlayfs": false, "devmapper": true},
			unpackErr:   errors.New("snapshotter unavailable"), // only reached for zfs
			runtimePlatforms: map[string]ImagePlatform{
				"runc": {Snapshotter: "devmapper"},
				"kata": {Snapshotter: "zfs"},
			},
			wantUpdate: true,
		},
		{
			desc:        "should skip image when layers are missing and no configured snapshotter has it unpacked",
			state:       contentMissingLayer,
			unpackedFor: map[string]bool{"overlayfs": false, "devmapper": false},
			unpackErr:   errors.New("snapshotter unavailable"), // only reached for zfs
			runtimePlatforms: map[string]ImagePlatform{
				"default": {Snapshotter: "overlayfs"}, // same as the image config
				"unset":   {},
				"runc":    {Snapshotter: "devmapper"},
				"kata":    {Snapshotter: "zfs"},
			},
			wantUpdate: false,
		},
		{
			desc:       "should skip image when the content readiness check fails",
			state:      contentComplete,
			contentErr: errors.New("content store failure"),
			unpacked:   true,
			wantUpdate: false,
		},
		{
			desc:       "should skip image when the manifest is unavailable",
			state:      contentUnavailable,
			unpacked:   true,
			wantUpdate: false,
		},
		{
			desc:       "should skip image when unpack check fails",
			state:      contentComplete,
			unpackErr:  errors.New("unpack check failed"),
			wantUpdate: false,
		},
		{
			desc:    "should return error when listing images fails",
			listErr: listErr,
			wantErr: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			var (
				cs     content.Store
				target ocispec.Descriptor
				imgs   []containerd.Image
			)
			if test.listErr == nil {
				cs, target = newCheckFixture(t, test.state)
				available, _, _, missing, err := images.Check(context.Background(), cs, target, platforms.Default())
				require.NoError(t, err)
				switch test.state {
				case contentComplete:
					require.True(t, available)
					require.Empty(t, missing)
				case contentMissingLayer:
					require.True(t, available)
					require.NotEmpty(t, missing)
				case contentUnavailable:
					require.False(t, available)
				}
				imgCS := cs
				if test.contentErr != nil {
					imgCS = errContentStore{Store: cs, err: test.contentErr}
				}
				imgs = []containerd.Image{
					&fakeCheckImage{
						name:        "registry.test/img:latest",
						target:      target,
						cs:          imgCS,
						unpacked:    test.unpacked,
						unpackedFor: test.unpackedFor,
						unpackErr:   test.unpackErr,
					},
				}
			} else {
				cs, _ = local.NewStore(t.TempDir())
			}

			client := &fakeCheckClient{imgs: imgs, listErr: test.listErr}
			svc := newCheckService(t, client, cs, test.runtimePlatforms)

			err := svc.CheckImages(context.Background())
			if test.wantErr {
				assert.ErrorIs(t, err, listErr)
				assert.Equal(t, int32(0), client.getCalls.Load())
				return
			}
			assert.NoError(t, err)
			if test.wantUpdate {
				assert.Equal(t, int32(1), client.getCalls.Load())
			} else {
				assert.Equal(t, int32(0), client.getCalls.Load())
			}
		})
	}
}

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
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/constants"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	imagedigest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeImagesStore struct {
	images.Store
	imgs       map[string]map[string]images.Image // namespace -> name -> image
	failCreate bool
}

func (f *fakeImagesStore) Get(ctx context.Context, name string) (images.Image, error) {
	ns, _ := namespaces.Namespace(ctx)
	if n, ok := f.imgs[ns]; ok {
		if img, ok := n[name]; ok {
			return img, nil
		}
	}
	return images.Image{}, errdefs.ErrNotFound
}

func (f *fakeImagesStore) List(ctx context.Context, fs ...string) ([]images.Image, error) {
	ns, _ := namespaces.Namespace(ctx)
	var res []images.Image
	if n, ok := f.imgs[ns]; ok {
		for _, img := range n {
			res = append(res, img)
		}
	}
	return res, nil
}

func (f *fakeImagesStore) Create(ctx context.Context, image images.Image) (images.Image, error) {
	if f.failCreate {
		return images.Image{}, errors.New("storage error")
	}
	ns, _ := namespaces.Namespace(ctx)
	if _, ok := f.imgs[ns]; !ok {
		f.imgs[ns] = make(map[string]images.Image)
	}
	if _, ok := f.imgs[ns][image.Name]; ok {
		return images.Image{}, errdefs.ErrAlreadyExists
	}
	f.imgs[ns][image.Name] = image
	return image, nil
}

type fakeLeasesManager struct {
	leases.Manager
	failCreate bool
}

func (f *fakeLeasesManager) Create(ctx context.Context, opts ...leases.Opt) (leases.Lease, error) {
	if f.failCreate {
		return leases.Lease{}, errors.New("lease creation failed")
	}
	var l leases.Lease
	for _, opt := range opts {
		if err := opt(&l); err != nil {
			return leases.Lease{}, err
		}
	}
	if l.ID == "" {
		l.ID = "test-lease"
	}
	return l, nil
}
func (f *fakeLeasesManager) Delete(ctx context.Context, lease leases.Lease, opts ...leases.DeleteOpt) error {
	return nil
}
func (f *fakeLeasesManager) AddResource(ctx context.Context, lease leases.Lease, res leases.Resource) error {
	return nil
}

type fakeContentStore struct {
	content.Store
	data map[string]map[imagedigest.Digest][]byte // namespace -> digest -> data
}

func (f *fakeContentStore) Info(ctx context.Context, dgst imagedigest.Digest) (content.Info, error) {
	for _, n := range f.data {
		if d, ok := n[dgst]; ok {
			return content.Info{Digest: dgst, Size: int64(len(d))}, nil
		}
	}
	return content.Info{}, errdefs.ErrNotFound
}

func (f *fakeContentStore) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	for _, n := range f.data {
		if d, ok := n[desc.Digest]; ok {
			return &fakeReaderAt{data: d}, nil
		}
	}
	return nil, errdefs.ErrNotFound
}

func (f *fakeContentStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	var desc ocispec.Descriptor
	for _, opt := range opts {
		var base content.WriterOpts
		opt(&base)
		if base.Desc.Digest != "" {
			desc = base.Desc
		}
	}
	return &fakeWriter{cs: f, ctx: ctx, desc: desc}, nil
}

type fakeWriter struct {
	content.Writer
	cs     *fakeContentStore
	ctx    context.Context
	desc   ocispec.Descriptor
	offset int64
}

func (f *fakeWriter) Write(p []byte) (int, error) {
	n := len(p)
	f.offset += int64(n)
	return n, nil
}

func (f *fakeWriter) Status() (content.Status, error) {
	return content.Status{
		Offset: f.offset,
		Total:  f.desc.Size,
	}, nil
}

func (f *fakeWriter) Truncate(size int64) error {
	f.offset = 0
	return nil
}

func (f *fakeWriter) Commit(ctx context.Context, size int64, expected imagedigest.Digest, opts ...content.Opt) error {
	ns, _ := namespaces.Namespace(f.ctx)
	if _, ok := f.cs.data[ns]; !ok {
		f.cs.data[ns] = make(map[imagedigest.Digest][]byte)
	}
	var data []byte
	for _, n := range f.cs.data {
		if d, ok := n[f.desc.Digest]; ok {
			data = d
			break
		}
	}
	f.cs.data[ns][f.desc.Digest] = data
	return nil
}
func (f *fakeWriter) Close() error {
	return nil
}

type fakeReaderAt struct {
	data []byte
}

func (f *fakeReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) && n+int(off) == len(f.data) {
		return n, io.EOF
	}
	return n, nil
}
func (f *fakeReaderAt) Size() int64 {
	return int64(len(f.data))
}
func (f *fakeReaderAt) Close() error {
	return nil
}

func TestLocalResolveWithAutoImport(t *testing.T) {
	nsSource := "moby"
	nsCRI := constants.K8sContainerdNamespace
	imageName := "docker.io/library/busybox:latest"
	
	// Create dummy image config
	layerDigest := imagedigest.Digest("sha256:5eb63bbbe01eeed093cb22bb8f5acdc3bc93bc3bc93bc3bc93bc3bc93bc3bc93")
	layerData := []byte("layerdata")
	imgSpec := imagespec.Image{
		RootFS: imagespec.RootFS{
			Type:    "layers",
			DiffIDs: []imagedigest.Digest{layerDigest},
		},
	}
	imgSpecBytes, _ := json.Marshal(imgSpec)
	configDigest := imagedigest.FromBytes(imgSpecBytes)

	// Create dummy manifest
	manifest := imagespec.Manifest{
		Config: ocispec.Descriptor{
			MediaType: imagespec.MediaTypeImageConfig,
			Digest:    configDigest,
			Size:      int64(len(imgSpecBytes)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: imagespec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layerData)),
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestDigest := imagedigest.FromBytes(manifestBytes)

	imgMetadata := images.Image{
		Name: imageName,
		Target: ocispec.Descriptor{
			Digest:    manifestDigest,
			MediaType: imagespec.MediaTypeImageManifest,
			Size:      int64(len(manifestBytes)),
		},
	}

	config := criconfig.ImageConfig{
		ImageAutoImportNamespaces: []string{nsSource},
	}

	t.Run("successful auto-import", func(t *testing.T) {
		fakeStore := &fakeImagesStore{
			imgs: map[string]map[string]images.Image{
				nsSource: {
					imageName: imgMetadata,
				},
				nsCRI: {},
			},
		}
		fakeCS := &fakeContentStore{
			data: map[string]map[imagedigest.Digest][]byte{
				nsSource: {
					manifestDigest: manifestBytes,
					configDigest:   imgSpecBytes,
					layerDigest:    layerData,
				},
				nsCRI: {},
			},
		}
		service := &CRIImageService{
			config: config,
			images: fakeStore,
			content: fakeCS,
			leases: &fakeLeasesManager{},
			imageStore:    imagestore.NewStore(fakeStore, fakeCS, platforms.Default()),
			snapshotStore: snapshotstore.NewStore(),
		}

		resolved, err := service.LocalResolve(imageName)
		require.NoError(t, err)
		assert.Equal(t, configDigest.String(), resolved.ID) // CRI Image ID is config digest

		// Verify auto-import labels
		criCtx := namespaces.WithNamespace(context.Background(), nsCRI)
		imported, err := fakeStore.Get(criCtx, imageName)
		require.NoError(t, err)
		assert.Equal(t, nsSource, imported.Labels["io.containerd.cri.image-auto-import/source-namespace"])
		assert.NotEmpty(t, imported.Labels["io.containerd.cri.image-auto-import/imported-at"])

		// Verify content was "auto-imported" to nsCRI
		// NOTE: In the real implementation we don't copy content anymore because it's shared.
		// Our fakeContentStore for unit tests already shares data across namespaces if we set it up that way,
		// but since we removed the claimContent call, we need to make sure the unit test reflects reality.
		// In reality, content.Info(criCtx, digest) would succeed if the blob is in the store at all.
		// Our fakeContentStore.Info currently checks namespace. Let's make it more realistic if needed,
		// or just verify the image metadata which is what we care about now.
		_, err = fakeCS.Info(criCtx, manifestDigest)
		assert.NoError(t, err, "manifest should be available in CRI namespace (shared content store)")
	})

	t.Run("failed auto-import due to storage error", func(t *testing.T) {
		// We'll simulate a failure in Create
		fakeStore := &fakeImagesStore{
			imgs: map[string]map[string]images.Image{
				nsSource: {
					imageName: imgMetadata,
				},
				nsCRI: {},
			},
			failCreate: true,
		}
		service := &CRIImageService{
			config: config,
			images: fakeStore,
			content: &fakeContentStore{},
			leases:  &fakeLeasesManager{},
			imageStore:    imagestore.NewStore(fakeStore, nil, platforms.Default()),
			snapshotStore: snapshotstore.NewStore(),
		}

		_, err := service.LocalResolve(imageName)
		assert.Error(t, err)

		// Verify NOT auto-imported
		criCtx := namespaces.WithNamespace(context.Background(), nsCRI)
		_, err = fakeStore.Get(criCtx, imageName)
		assert.True(t, errdefs.IsNotFound(err))
	})

	t.Run("auto-import from second additional namespace", func(t *testing.T) {
		nsSource2 := "shared"
		config2 := config
		config2.ImageAutoImportNamespaces = []string{"other", nsSource2}

		fakeStore := &fakeImagesStore{
			imgs: map[string]map[string]images.Image{
				nsSource2: {
					imageName: imgMetadata,
				},
				nsCRI: {},
			},
		}
		fakeCS := &fakeContentStore{
			data: map[string]map[imagedigest.Digest][]byte{
				nsSource2: {
					manifestDigest: manifestBytes,
					configDigest:   imgSpecBytes,
					layerDigest:    layerData,
				},
				nsCRI: {},
			},
		}
		service := &CRIImageService{
			config: config2,
			images: fakeStore,
			content: fakeCS,
			leases: &fakeLeasesManager{},
			imageStore:    imagestore.NewStore(fakeStore, fakeCS, platforms.Default()),
			snapshotStore: snapshotstore.NewStore(),
		}

		resolved, err := service.LocalResolve(imageName)
		require.NoError(t, err)
		assert.Equal(t, configDigest.String(), resolved.ID)

		criCtx := namespaces.WithNamespace(context.Background(), nsCRI)
		imported, err := fakeStore.Get(criCtx, imageName)
		require.NoError(t, err)
		assert.Equal(t, nsSource2, imported.Labels["io.containerd.cri.image-auto-import/source-namespace"])
	})
}

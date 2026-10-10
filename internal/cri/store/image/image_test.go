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

package image

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/go-digest/digestset"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestInternalStore(t *testing.T) {
	images := []Image{
		{
			ID:         "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:    "test-chain-id-1",
			References: []string{"containerd.io/ref-1"},
			Size:       10,
		},
		{
			ID:         "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:    "test-chain-id-2abcd",
			References: []string{"containerd.io/ref-2abcd"},
			Size:       20,
		},
		{
			ID:         "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			References: []string{"containerd.io/ref-4a333"},
			ChainID:    "test-chain-id-4a333",
			Size:       30,
		},
		{
			ID:         "sha256:4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			References: []string{"containerd.io/ref-4abcd"},
			ChainID:    "test-chain-id-4abcd",
			Size:       40,
		},
	}
	assert := assertlib.New(t)
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }

	s := &store{
		images:     make(map[string]Image),
		digestSet:  digestset.NewSet(),
		pinnedRefs: make(map[string]sets.Set[string]),
	}

	t.Logf("should be able to add image")
	for _, img := range images {
		err := s.add(img)
		assert.NoError(err)
	}

	t.Logf("should be able to get image")
	for _, v := range images {
		truncID := genTruncIndex(v.ID)
		got, err := s.get(truncID)
		assert.NoError(err, "truncID:%s, fullID:%s", truncID, v.ID)
		assert.Equal(v, got)
	}

	t.Logf("should be able to get image by truncated imageId without algorithm")
	for _, v := range images {
		truncID := genTruncIndex(v.ID[strings.Index(v.ID, ":")+1:])
		got, err := s.get(truncID)
		assert.NoError(err, "truncID:%s, fullID:%s", truncID, v.ID)
		assert.Equal(v, got)
	}

	t.Logf("should not be able to get image by ambiguous prefix")
	ambiguousPrefixs := []string{"sha256", "sha256:"}
	for _, v := range ambiguousPrefixs {
		_, err := s.get(v)
		assert.NotEqual(nil, err)
	}

	t.Logf("should be able to list images")
	imgs := s.list()
	assert.Len(imgs, len(images))

	imageNum := len(images)
	for _, v := range images {
		truncID := genTruncIndex(v.ID)
		oldRef := v.References[0]
		newRef := oldRef + "new"

		t.Logf("should be able to add new references")
		newImg := v
		newImg.References = []string{newRef}
		err := s.add(newImg)
		assert.NoError(err)
		got, err := s.get(truncID)
		assert.NoError(err)
		assert.Len(got.References, 2)
		assert.Contains(got.References, oldRef, newRef)

		t.Logf("should not be able to add duplicated references")
		err = s.add(newImg)
		assert.NoError(err)
		got, err = s.get(truncID)
		assert.NoError(err)
		assert.Len(got.References, 2)
		assert.Contains(got.References, oldRef, newRef)

		t.Logf("should be able to delete image references")
		s.delete(truncID, oldRef)
		got, err = s.get(truncID)
		assert.NoError(err)
		assert.Equal([]string{newRef}, got.References)

		t.Logf("should be able to delete image")
		s.delete(truncID, newRef)
		got, err = s.get(truncID)
		assert.Equal(errdefs.ErrNotFound, err)
		assert.Equal(Image{}, got)

		imageNum--
		imgs = s.list()
		assert.Len(imgs, imageNum)
	}
}

func TestInternalStorePinnedImage(t *testing.T) {
	assert := assertlib.New(t)
	s := &store{
		images:     make(map[string]Image),
		digestSet:  digestset.NewSet(),
		pinnedRefs: make(map[string]sets.Set[string]),
	}

	ref1 := "containerd.io/ref-1"
	image := Image{
		ID:         "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ChainID:    "test-chain-id-1",
		References: []string{ref1},
		Size:       10,
	}

	t.Logf("add unpinned image ref, image should be unpinned")
	assert.NoError(s.add(image))
	i, err := s.get(image.ID)
	assert.NoError(err)
	assert.False(i.Pinned)
	assert.False(s.isPinned(image.ID, ref1))

	t.Logf("add pinned image ref, image should be pinned")
	ref2 := "containerd.io/ref-2"
	image.References = []string{ref2}
	image.Pinned = true
	assert.NoError(s.add(image))
	i, err = s.get(image.ID)
	assert.NoError(err)
	assert.True(i.Pinned)
	assert.False(s.isPinned(image.ID, ref1))
	assert.True(s.isPinned(image.ID, ref2))

	t.Logf("pin unpinned image ref, image should be pinned, all refs should be pinned")
	assert.NoError(s.pin(image.ID, ref1))
	i, err = s.get(image.ID)
	assert.NoError(err)
	assert.True(i.Pinned)
	assert.True(s.isPinned(image.ID, ref1))
	assert.True(s.isPinned(image.ID, ref2))

	t.Logf("unpin one of image refs, image should be pinned")
	assert.NoError(s.unpin(image.ID, ref2))
	i, err = s.get(image.ID)
	assert.NoError(err)
	assert.True(i.Pinned)
	assert.True(s.isPinned(image.ID, ref1))
	assert.False(s.isPinned(image.ID, ref2))

	t.Logf("unpin the remaining one image ref, image should be unpinned")
	assert.NoError(s.unpin(image.ID, ref1))
	i, err = s.get(image.ID)
	assert.NoError(err)
	assert.False(i.Pinned)
	assert.False(s.isPinned(image.ID, ref1))
	assert.False(s.isPinned(image.ID, ref2))

	t.Logf("pin one of image refs, then delete this, image should be unpinned")
	assert.NoError(s.pin(image.ID, ref1))
	s.delete(image.ID, ref1)
	i, err = s.get(image.ID)
	assert.NoError(err)
	assert.False(i.Pinned)
	assert.False(s.isPinned(image.ID, ref2))
}

func TestImageStore(t *testing.T) {
	id := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	newID := "sha256:9923456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	image := Image{
		ID:         id,
		ChainID:    "test-chain-id-1",
		References: []string{"containerd.io/ref-1"},
		Size:       10,
	}
	assert := assertlib.New(t)

	equal := func(i1, i2 Image) {
		sort.Strings(i1.References)
		sort.Strings(i2.References)
		assert.Equal(i1, i2)
	}
	for desc, test := range map[string]struct {
		ref      string
		image    *Image
		expected []Image
	}{
		"nothing should happen if a non-exist ref disappear": {
			ref:      "containerd.io/ref-2",
			image:    nil,
			expected: []Image{image},
		},
		"new ref for an existing image": {
			ref: "containerd.io/ref-2",
			image: &Image{
				ID:         id,
				ChainID:    "test-chain-id-1",
				References: []string{"containerd.io/ref-2"},
				Size:       10,
			},
			expected: []Image{
				{
					ID:         id,
					ChainID:    "test-chain-id-1",
					References: []string{"containerd.io/ref-1", "containerd.io/ref-2"},
					Size:       10,
				},
			},
		},
		"new ref for a new image": {
			ref: "containerd.io/ref-2",
			image: &Image{
				ID:         newID,
				ChainID:    "test-chain-id-2",
				References: []string{"containerd.io/ref-2"},
				Size:       20,
			},
			expected: []Image{
				image,
				{
					ID:         newID,
					ChainID:    "test-chain-id-2",
					References: []string{"containerd.io/ref-2"},
					Size:       20,
				},
			},
		},
		"existing ref point to a new image": {
			ref: "containerd.io/ref-1",
			image: &Image{
				ID:         newID,
				ChainID:    "test-chain-id-2",
				References: []string{"containerd.io/ref-1"},
				Size:       20,
			},
			expected: []Image{
				{
					ID:         newID,
					ChainID:    "test-chain-id-2",
					References: []string{"containerd.io/ref-1"},
					Size:       20,
				},
			},
		},
		"existing ref disappear": {
			ref:      "containerd.io/ref-1",
			image:    nil,
			expected: []Image{},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			s, err := NewFakeStore([]Image{image})
			assert.NoError(err)
			assert.NoError(s.update(test.ref, test.image))

			assert.Len(s.List(), len(test.expected))
			for _, expect := range test.expected {
				got, err := s.Get(expect.ID)
				assert.NoError(err)
				equal(got, expect)
				for _, ref := range expect.References {
					id, err := s.Resolve(ref)
					assert.NoError(err)
					assert.Equal(expect.ID, id)
				}
			}

			if test.image == nil {
				// Shouldn't be able to index by removed ref.
				id, err := s.Resolve(test.ref)
				assert.Equal(errdefs.ErrNotFound, err)
				assert.Empty(id)
			}
		})
	}
}

type fakeGetter struct {
	imgs map[string]images.Image
	errs map[string]error
}

func (f *fakeGetter) Get(ctx context.Context, name string) (images.Image, error) {
	if f.errs != nil {
		if err, ok := f.errs[name]; ok {
			return images.Image{}, err
		}
	}
	img, ok := f.imgs[name]
	if !ok {
		return images.Image{}, errdefs.ErrNotFound
	}
	return img, nil
}

type fakeContentProvider struct {
	err error
}

func (f *fakeContentProvider) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	if f.err != nil {
		return content.Info{}, f.err
	}
	return content.Info{}, errdefs.ErrNotFound
}

func (f *fakeContentProvider) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, errdefs.ErrNotFound
}

func TestStoreUpdateMissingContent(t *testing.T) {
	assert := assertlib.New(t)
	ctx := context.Background()
	imgID := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ref := "containerd.io/ref-missing-content"

	getter := &fakeGetter{
		imgs: map[string]images.Image{
			ref: {
				Name: ref,
				Target: ocispec.Descriptor{
					Digest: digest.Digest(imgID),
				},
			},
		},
	}
	provider := &fakeContentProvider{err: errdefs.ErrNotFound}

	s := NewStore(getter, provider, platforms.Default())
	// Pre-populate store cache with image
	s.refCache[ref] = imgID
	assert.NoError(s.store.add(Image{
		ID:         imgID,
		References: []string{ref},
	}))

	// Verify image exists in cache before Update
	_, err := s.Get(imgID)
	assert.NoError(err)

	// Update should catch missing content (ErrNotFound from getImage) and purge ref from cache
	err = s.Update(ctx, ref)
	assert.NoError(err)

	// Image should no longer exist in cache
	_, err = s.Get(imgID)
	assert.Equal(errdefs.ErrNotFound, err)
	_, err = s.Resolve(ref)
	assert.Equal(errdefs.ErrNotFound, err)
}

func TestStoreUpdateStaleReferences(t *testing.T) {
	assert := assertlib.New(t)
	ctx := context.Background()
	imgID := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ref1 := "containerd.io/primary"
	ref2 := "containerd.io/extra@sha256:1123456789abcdef"

	// containerd store has NO images left (both ref1 and ref2 were deleted outside CRI)
	getter := &fakeGetter{
		imgs: map[string]images.Image{},
	}
	provider := &fakeContentProvider{}

	s := NewStore(getter, provider, platforms.Default())
	// Cache contains image with BOTH references
	s.refCache[ref1] = imgID
	s.refCache[ref2] = imgID
	assert.NoError(s.store.add(Image{
		ID:         imgID,
		References: []string{ref1, ref2},
	}))

	// Update called for ref1
	err := s.Update(ctx, ref1)
	assert.NoError(err)

	// Since ref2 is ALSO deleted from containerd, s.Update should clean up all stale references and remove imgID
	_, err = s.Get(imgID)
	assert.Equal(errdefs.ErrNotFound, err)
	_, err = s.Resolve(ref1)
	assert.Equal(errdefs.ErrNotFound, err)
	_, err = s.Resolve(ref2)
	assert.Equal(errdefs.ErrNotFound, err)
}

func TestStoreUpdateTransientError(t *testing.T) {
	assert := assertlib.New(t)
	ctx := context.Background()
	imgID := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ref1 := "containerd.io/primary"
	ref2 := "containerd.io/extra@sha256:1123456789abcdef"

	transientErr := errors.New("transient database connection error")
	getter := &fakeGetter{
		imgs: map[string]images.Image{},
		errs: map[string]error{
			ref2: transientErr,
		},
	}
	provider := &fakeContentProvider{}

	s := NewStore(getter, provider, platforms.Default())
	s.refCache[ref1] = imgID
	s.refCache[ref2] = imgID
	assert.NoError(s.store.add(Image{
		ID:         imgID,
		References: []string{ref1, ref2},
	}))

	// Update called for ref1 (which was deleted), but checking ref2 returns a transient error
	err := s.Update(ctx, ref1)
	assert.Error(err)
	assert.True(errors.Is(err, transientErr) || strings.Contains(err.Error(), transientErr.Error()))

	// ref2 was NOT confirmed missing due to the transient error, so ref2 must NOT be purged from cache
	resolvedID, err := s.Resolve(ref2)
	assert.NoError(err)
	assert.Equal(imgID, resolvedID)
}

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
	"sort"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/internal/cri/setutils"
	"github.com/containerd/errdefs"

	"github.com/opencontainers/go-digest/digestset"
	assertlib "github.com/stretchr/testify/assert"
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
		pinnedRefs: make(map[string]setutils.Set[string]),
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
		pinnedRefs: make(map[string]setutils.Set[string]),
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

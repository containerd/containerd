/*
Copyright 2017 The Kubernetes Authors.

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
	"strings"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	assertlib "github.com/stretchr/testify/assert"

	"github.com/containerd/cri/pkg/store"
)

func TestImageStore(t *testing.T) {
	images := []Image{
		{
			ID:          "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:     "test-chain-id-1",
			RepoTags:    []string{"tag-1"},
			RepoDigests: []string{"digest-1"},
			Size:        10,
			ImageSpec:   imagespec.Image{},
		},
		{
			ID:          "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:     "test-chain-id-2abcd",
			RepoTags:    []string{"tag-2abcd"},
			RepoDigests: []string{"digest-2abcd"},
			Size:        20,
			ImageSpec:   imagespec.Image{},
		},
		{
			ID:          "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"tag-4a333"},
			RepoDigests: []string{"digest-4a333"},
			ChainID:     "test-chain-id-4a333",
			Size:        30,
			ImageSpec:   imagespec.Image{},
		},
		{
			ID:          "sha256:4123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"tag-4abcd"},
			RepoDigests: []string{"digest-4abcd"},
			ChainID:     "test-chain-id-4abcd",
			Size:        40,
			ImageSpec:   imagespec.Image{},
		},
	}
	assert := assertlib.New(t)
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }

	s := NewStore()

	t.Logf("should be able to add image")
	for _, img := range images {
		err := s.Add(img)
		assert.NoError(err)
	}

	t.Logf("should be able to get image")
	for _, v := range images {
		truncID := genTruncIndex(v.ID)
		got, err := s.Get(truncID)
		assert.NoError(err, "truncID:%s, fullID:%s", truncID, v.ID)
		assert.Equal(v, got)
	}

	t.Logf("should be able to get image by truncated imageId without algorithm")
	for _, v := range images {
		truncID := genTruncIndex(v.ID[strings.Index(v.ID, ":")+1:])
		got, err := s.Get(truncID)
		assert.NoError(err, "truncID:%s, fullID:%s", truncID, v.ID)
		assert.Equal(v, got)
	}

	t.Logf("should not be able to get image by ambiguous prefix")
	ambiguousPrefixs := []string{"sha256", "sha256:"}
	for _, v := range ambiguousPrefixs {
		_, err := s.Get(v)
		assert.NotEqual(nil, err)
	}

	t.Logf("should be able to list images")
	imgs := s.List()
	assert.Len(imgs, len(images))

	imageNum := len(images)
	for _, v := range images {
		truncID := genTruncIndex(v.ID)
		oldRepoTag := v.RepoTags[0]
		oldRepoDigest := v.RepoDigests[0]
		newRepoTag := oldRepoTag + "new"
		newRepoDigest := oldRepoDigest + "new"

		t.Logf("should be able to add new repo tags/digests")
		newImg := v
		newImg.RepoTags = []string{newRepoTag}
		newImg.RepoDigests = []string{newRepoDigest}
		err := s.Add(newImg)
		assert.NoError(err)
		got, err := s.Get(truncID)
		assert.NoError(err)
		assert.Len(got.RepoTags, 2)
		assert.Contains(got.RepoTags, oldRepoTag, newRepoTag)
		assert.Len(got.RepoDigests, 2)
		assert.Contains(got.RepoDigests, oldRepoDigest, newRepoDigest)

		t.Logf("should not be able to add duplicated repo tags/digests")
		err = s.Add(newImg)
		assert.NoError(err)
		got, err = s.Get(truncID)
		assert.NoError(err)
		assert.Len(got.RepoTags, 2)
		assert.Contains(got.RepoTags, oldRepoTag, newRepoTag)
		assert.Len(got.RepoDigests, 2)
		assert.Contains(got.RepoDigests, oldRepoDigest, newRepoDigest)

		t.Logf("should be able to delete image")
		s.Delete(truncID)
		imageNum--
		imgs = s.List()
		assert.Len(imgs, imageNum)

		t.Logf("get should return empty struct and ErrNotExist after deletion")
		img, err := s.Get(truncID)
		assert.Equal(Image{}, img)
		assert.Equal(store.ErrNotExist, err)
	}
}

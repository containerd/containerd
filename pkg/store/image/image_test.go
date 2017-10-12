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
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	assertlib "github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

func TestImageStore(t *testing.T) {
	images := map[string]Image{
		"1": {
			ID:          "1",
			ChainID:     "test-chain-id-1",
			RepoTags:    []string{"tag-1"},
			RepoDigests: []string{"digest-1"},
			Size:        10,
			Config:      &imagespec.ImageConfig{},
		},
		"2abcd": {
			ID:          "2abcd",
			ChainID:     "test-chain-id-2abcd",
			RepoTags:    []string{"tag-2abcd"},
			RepoDigests: []string{"digest-2abcd"},
			Size:        20,
			Config:      &imagespec.ImageConfig{},
		},
		"4a333": {
			ID:          "4a333",
			RepoTags:    []string{"tag-4a333"},
			RepoDigests: []string{"digest-4a333"},
			ChainID:     "test-chain-id-4a333",
			Size:        30,
			Config:      &imagespec.ImageConfig{},
		},
		"4abcd": {
			ID:          "4abcd",
			RepoTags:    []string{"tag-4abcd"},
			RepoDigests: []string{"digest-4abcd"},
			ChainID:     "test-chain-id-4abcd",
			Size:        40,
			Config:      &imagespec.ImageConfig{},
		},
	}
	assert := assertlib.New(t)

	s := NewStore()

	t.Logf("should be able to add image")
	for _, img := range images {
		err := s.Add(img)
		assert.NoError(err)
	}

	t.Logf("should be able to get image")
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }
	for id, img := range images {
		got, err := s.Get(genTruncIndex(id))
		assert.NoError(err)
		assert.Equal(img, got)
	}

	t.Logf("should be able to list images")
	imgs := s.List()
	assert.Len(imgs, len(images))

	imageNum := len(images)
	for testID, v := range images {
		truncID := genTruncIndex(testID)
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

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
		"2": {
			ID:          "2",
			ChainID:     "test-chain-id-2",
			RepoTags:    []string{"tag-2"},
			RepoDigests: []string{"digest-2"},
			Size:        20,
			Config:      &imagespec.ImageConfig{},
		},
		"3": {
			ID:          "3",
			RepoTags:    []string{"tag-3"},
			RepoDigests: []string{"digest-3"},
			ChainID:     "test-chain-id-3",
			Size:        30,
			Config:      &imagespec.ImageConfig{},
		},
	}
	assert := assertlib.New(t)

	s := NewStore()

	t.Logf("should be able to add image")
	for _, img := range images {
		s.Add(img)
	}

	t.Logf("should be able to get image")
	for id, img := range images {
		got, err := s.Get(id)
		assert.NoError(err)
		assert.Equal(img, got)
	}

	t.Logf("should be able to list images")
	imgs := s.List()
	assert.Len(imgs, 3)

	testID := "2"
	t.Logf("should be able to add new repo tags/digests")
	newImg := images[testID]
	newImg.RepoTags = []string{"tag-new"}
	newImg.RepoDigests = []string{"digest-new"}
	s.Add(newImg)
	got, err := s.Get(testID)
	assert.NoError(err)
	assert.Len(got.RepoTags, 2)
	assert.Contains(got.RepoTags, "tag-2", "tag-new")
	assert.Len(got.RepoDigests, 2)
	assert.Contains(got.RepoDigests, "digest-2", "digest-new")

	t.Logf("should not be able to add duplicated repo tags/digests")
	s.Add(newImg)
	got, err = s.Get(testID)
	assert.NoError(err)
	assert.Len(got.RepoTags, 2)
	assert.Contains(got.RepoTags, "tag-2", "tag-new")
	assert.Len(got.RepoDigests, 2)
	assert.Contains(got.RepoDigests, "digest-2", "digest-new")

	t.Logf("should be able to delete image")
	s.Delete(testID)
	imgs = s.List()
	assert.Len(imgs, 2)

	t.Logf("get should return empty struct and ErrNotExist after deletion")
	img, err := s.Get(testID)
	assert.Equal(Image{}, img)
	assert.Equal(store.ErrNotExist, err)
}

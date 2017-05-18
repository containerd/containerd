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

package metadata

import (
	"testing"

	assertlib "github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
)

func TestImageMetadataStore(t *testing.T) {
	imageMetadataMap := map[string]*ImageMetadata{
		"1": {
			ID:   "1",
			Size: 10,
		},
		"2": {
			ID:   "2",
			Size: 20,
		},
		"3": {
			ID:   "3",
			Size: 30,
		},
	}
	assert := assertlib.New(t)

	s := NewImageMetadataStore(store.NewMetadataStore())

	t.Logf("should be able to create image metadata")
	for _, meta := range imageMetadataMap {
		assert.NoError(s.Create(*meta))
	}

	t.Logf("should be able to get image metadata")
	for id, expectMeta := range imageMetadataMap {
		meta, err := s.Get(id)
		assert.NoError(err)
		assert.Equal(expectMeta, meta)
	}

	t.Logf("should be able to list image metadata")
	imgs, err := s.List()
	assert.NoError(err)
	assert.Len(imgs, 3)

	t.Logf("should be able to update image metadata")
	testID := "2"
	newSize := int64(200)
	expectMeta := *imageMetadataMap[testID]
	expectMeta.Size = newSize
	err = s.Update(testID, func(o ImageMetadata) (ImageMetadata, error) {
		o.Size = newSize
		return o, nil
	})
	assert.NoError(err)
	newMeta, err := s.Get(testID)
	assert.NoError(err)
	assert.Equal(&expectMeta, newMeta)

	t.Logf("should be able to delete image metadata")
	assert.NoError(s.Delete(testID))
	imgs, err = s.List()
	assert.NoError(err)
	assert.Len(imgs, 2)

	t.Logf("get should return nil not exist error after deletion")
	meta, err := s.Get(testID)
	assert.Error(store.ErrNotExist, err)
	assert.Nil(meta)
}

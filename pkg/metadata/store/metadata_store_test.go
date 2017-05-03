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

package store

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	assertlib "github.com/stretchr/testify/assert"
)

func TestMetadata(t *testing.T) {
	testData := [][]byte{
		[]byte("test-data-1"),
		[]byte("test-data-2"),
	}
	updateErr := errors.New("update error")
	assert := assertlib.New(t)

	t.Logf("simple create and get")
	meta, err := newMetadata(testData[0])
	assert.NoError(err)
	old := meta.get()
	assert.Equal(testData[0], old)

	t.Logf("failed update should not take effect")
	err = meta.update(func(in []byte) ([]byte, error) {
		return testData[1], updateErr
	})
	assert.Equal(updateErr, err)
	assert.Equal(testData[0], meta.get())

	t.Logf("successful update should take effect")
	err = meta.update(func(in []byte) ([]byte, error) {
		return testData[1], nil
	})
	assert.NoError(err)
	assert.Equal(testData[1], meta.get())

	t.Logf("successful update should not affect existing snapshot")
	assert.Equal(testData[0], old)

	// TODO(random-liu): Test deleteCheckpoint and cleanupCheckpoint after
	// disk based implementation is added.
}

func TestMetadataStore(t *testing.T) {
	testIds := []string{"id-0", "id-1"}
	testMeta := map[string][]byte{
		testIds[0]: []byte("metadata-0"),
		testIds[1]: []byte("metadata-1"),
	}
	assert := assertlib.New(t)

	m := NewMetadataStore()

	t.Logf("should be empty initially")
	metas, err := m.List()
	assert.NoError(err)
	assert.Empty(metas)

	t.Logf("should be able to create metadata")
	err = m.Create(testIds[0], testMeta[testIds[0]])
	assert.NoError(err)

	t.Logf("should not be able to create metadata with the same id")
	err = m.Create(testIds[0], testMeta[testIds[0]])
	assert.Error(err)

	t.Logf("should be able to list metadata")
	err = m.Create(testIds[1], testMeta[testIds[1]])
	assert.NoError(err)
	metas, err = m.List()
	assert.NoError(err)
	assert.True(sliceContainsMap(metas, testMeta))

	t.Logf("should be able to get metadata by id")
	meta, err := m.Get(testIds[1])
	assert.NoError(err)
	assert.Equal(testMeta[testIds[1]], meta)

	t.Logf("update should take effect")
	m.Update(testIds[1], func(in []byte) ([]byte, error) {
		return []byte("updated-metadata-1"), nil
	})
	newMeta, err := m.Get(testIds[1])
	assert.NoError(err)
	assert.Equal([]byte("updated-metadata-1"), newMeta)

	t.Logf("should be able to delete metadata")
	assert.NoError(m.Delete(testIds[1]))
	metas, err = m.List()
	assert.NoError(err)
	assert.Len(metas, 1)
	assert.Equal(testMeta[testIds[0]], metas[0])
	meta, err = m.Get(testIds[1])
	assert.NoError(err)
	assert.Nil(meta)

	t.Logf("existing reference should not be affected by delete")
	assert.Equal([]byte("updated-metadata-1"), newMeta)

	t.Logf("should be able to reuse the same id after deletion")
	err = m.Create(testIds[1], testMeta[testIds[1]])
	assert.NoError(err)
}

// sliceMatchMap checks the same elements with a map.
func sliceContainsMap(s [][]byte, m map[string][]byte) bool {
	if len(m) != len(s) {
		return false
	}
	for _, expect := range m {
		found := false
		for _, got := range s {
			if bytes.Equal(expect, got) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestMultithreadAccess(t *testing.T) {
	m := NewMetadataStore()
	assert := assertlib.New(t)
	routineNum := 10
	var wg sync.WaitGroup
	for i := 0; i < routineNum; i++ {
		wg.Add(1)
		go func(i int) {
			id := fmt.Sprintf("%d", i)

			t.Logf("should be able to create id %q", id)
			expect := []byte(id)
			err := m.Create(id, expect)
			assert.NoError(err)

			got, err := m.Get(id)
			assert.NoError(err)
			assert.Equal(expect, got)

			gotList, err := m.List()
			assert.NoError(err)
			assert.Contains(gotList, expect)

			t.Logf("should be able to update id %q", id)
			expect = []byte("update-" + id)
			err = m.Update(id, func([]byte) ([]byte, error) {
				return expect, nil
			})
			assert.NoError(err)

			got, err = m.Get(id)
			assert.NoError(err)
			assert.Equal(expect, got)

			t.Logf("should be able to delete id %q", id)
			err = m.Delete(id)
			assert.NoError(err)

			got, err = m.Get(id)
			assert.NoError(err)
			assert.Nil(got)

			gotList, err = m.List()
			assert.NoError(err)
			assert.NotContains(gotList, expect)

			wg.Done()
		}(i)
	}
	wg.Wait()
}

// TODO(random-liu): Test recover logic once checkpoint recovery is added.

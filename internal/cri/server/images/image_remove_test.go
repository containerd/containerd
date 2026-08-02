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
	"errors"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/images"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
)

// fakeImageStore records the order in which references are deleted and can fail
// after a given number of deletions to emulate an interrupted removal.
type fakeImageStore struct {
	images.Store

	deleted   []string
	failAfter int
	err       error
}

func (f *fakeImageStore) Delete(_ context.Context, name string, _ ...images.DeleteOpt) error {
	if f.failAfter > 0 && len(f.deleted) >= f.failAfter {
		return f.err
	}
	f.deleted = append(f.deleted, name)
	return nil
}

// Get satisfies the image store Getter used to refresh the CRI image cache. Every
// reference is reported as gone, which is the state after a successful delete.
func (f *fakeImageStore) Get(_ context.Context, _ string) (images.Image, error) {
	return images.Image{}, errdefs.ErrNotFound
}

const (
	testRemoveImageID   = "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799"
	testRemoveRepoTag   = "docker.io/library/busybox:latest"
	testRemoveRepoDigst = "docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"
)

func addTestImage(t *testing.T, c *CRIImageService, refs []string, getter imagestore.Getter) {
	t.Helper()
	img := imagestore.Image{
		ID:         testRemoveImageID,
		ChainID:    "test-chain-id",
		References: refs,
	}
	store, err := imagestore.NewFakeStoreWithGetter([]imagestore.Image{img}, getter)
	require.NoError(t, err)
	c.imageStore = store
}

func TestRemoveImageDeletesImageIDFirst(t *testing.T) {
	// The image ID reference is the only one that cannot be resolved by name, so it
	// must be removed first: an interrupted removal then always leaves a reference
	// that can still be found and removed again.
	c, _ := newTestCRIService()
	fake := &fakeImageStore{}
	c.images = fake

	// References are stored sorted, which places the bare config digest last.
	addTestImage(t, c, []string{testRemoveRepoTag, testRemoveRepoDigst, testRemoveImageID}, fake)

	err := c.RemoveImage(context.Background(), &runtime.ImageSpec{Image: testRemoveImageID})
	require.NoError(t, err)

	require.Len(t, fake.deleted, 3)
	assert.Equal(t, testRemoveImageID, fake.deleted[0], "image ID reference must be deleted first")
	assert.ElementsMatch(t, []string{testRemoveRepoTag, testRemoveRepoDigst}, fake.deleted[1:])
}

func TestRemoveImageInterruptedLeavesNamedReference(t *testing.T) {
	// An interrupted removal must not strand an ID-only record, which nothing can
	// reach by name afterwards.
	c, _ := newTestCRIService()
	fake := &fakeImageStore{failAfter: 1, err: errors.New("context deadline exceeded")}
	c.images = fake

	addTestImage(t, c, []string{testRemoveRepoTag, testRemoveRepoDigst, testRemoveImageID}, fake)

	err := c.RemoveImage(context.Background(), &runtime.ImageSpec{Image: testRemoveImageID})
	require.Error(t, err)

	require.Len(t, fake.deleted, 1)
	assert.Equal(t, testRemoveImageID, fake.deleted[0])
}

func TestImageIDFirst(t *testing.T) {
	for _, tc := range []struct {
		name     string
		refs     []string
		id       string
		expected []string
	}{
		{
			name:     "id last",
			refs:     []string{"tag", "digest", "id"},
			id:       "id",
			expected: []string{"id", "tag", "digest"},
		},
		{
			name:     "id already first",
			refs:     []string{"id", "tag"},
			id:       "id",
			expected: []string{"id", "tag"},
		},
		{
			name:     "id in the middle",
			refs:     []string{"tag", "id", "digest"},
			id:       "id",
			expected: []string{"id", "tag", "digest"},
		},
		{
			name:     "id absent",
			refs:     []string{"tag", "digest"},
			id:       "id",
			expected: []string{"tag", "digest"},
		},
		{
			name:     "only the id",
			refs:     []string{"id"},
			id:       "id",
			expected: []string{"id"},
		},
		{
			name:     "no references",
			refs:     nil,
			id:       "id",
			expected: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := slicesClone(tc.refs)
			assert.Equal(t, tc.expected, imageIDFirst(tc.refs, tc.id))
			assert.Equal(t, refs, tc.refs, "input must not be modified")
		})
	}
}

func slicesClone(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

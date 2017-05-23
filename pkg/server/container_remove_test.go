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

package server

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
)

// TestSetContainerRemoving tests setContainerRemoving sets removing
// state correctly.
func TestSetContainerRemoving(t *testing.T) {
	testID := "test-id"
	for desc, test := range map[string]struct {
		metadata  *metadata.ContainerMetadata
		expectErr bool
	}{
		"should return error when container is in running state": {
			metadata: &metadata.ContainerMetadata{
				ID:        testID,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			expectErr: true,
		},
		"should return error when container is in removing state": {
			metadata: &metadata.ContainerMetadata{
				ID:         testID,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
				Removing:   true,
			},
			expectErr: true,
		},
		"should not return error when container is not running and removing": {
			metadata: &metadata.ContainerMetadata{
				ID:         testID,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			expectErr: false,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		if test.metadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.metadata))
		}
		err := c.setContainerRemoving(testID)
		meta, getErr := c.containerStore.Get(testID)
		assert.NoError(t, getErr)
		if test.expectErr {
			assert.Error(t, err)
			assert.Equal(t, test.metadata, meta, "metadata should not be updated")
		} else {
			assert.NoError(t, err)
			assert.True(t, meta.Removing, "removing should be set")
		}
	}
}

func TestRemoveContainer(t *testing.T) {
	testID := "test-id"
	testName := "test-name"
	for desc, test := range map[string]struct {
		metadata            *metadata.ContainerMetadata
		removeDirErr        error
		expectErr           bool
		expectUnsetRemoving bool
	}{
		"should return error when container is still running": {
			metadata: &metadata.ContainerMetadata{
				ID:        testID,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			expectErr: true,
		},
		"should return error when there is ongoing removing": {
			metadata: &metadata.ContainerMetadata{
				ID:         testID,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
				Removing:   true,
			},
			expectErr: true,
		},
		"should not return error if container does not exist": {
			metadata:  nil,
			expectErr: false,
		},
		"should return error if remove container root fails": {
			metadata: &metadata.ContainerMetadata{
				ID:         testID,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			removeDirErr:        errors.New("random error"),
			expectErr:           true,
			expectUnsetRemoving: true,
		},
		"should be able to remove container successfully": {
			metadata: &metadata.ContainerMetadata{
				ID:         testID,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			expectErr: false,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fakeOS := c.os.(*ostesting.FakeOS)
		if test.metadata != nil {
			assert.NoError(t, c.containerNameIndex.Reserve(testName, testID))
			assert.NoError(t, c.containerStore.Create(*test.metadata))
		}
		fakeOS.RemoveAllFn = func(path string) error {
			assert.Equal(t, getContainerRootDir(c.rootDir, testID), path)
			return test.removeDirErr
		}
		resp, err := c.RemoveContainer(context.Background(), &runtime.RemoveContainerRequest{
			ContainerId: testID,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
			if !test.expectUnsetRemoving {
				continue
			}
			meta, err := c.containerStore.Get(testID)
			assert.NoError(t, err)
			require.NotNil(t, meta)
			// Also covers resetContainerRemoving.
			assert.False(t, meta.Removing, "removing state should be unset")
			continue
		}
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		meta, err := c.containerStore.Get(testID)
		assert.Error(t, err)
		assert.True(t, metadata.IsNotExistError(err))
		assert.Nil(t, meta, "container metadata should be removed")
		assert.NoError(t, c.containerNameIndex.Reserve(testName, testID),
			"container name should be released")
	}
}

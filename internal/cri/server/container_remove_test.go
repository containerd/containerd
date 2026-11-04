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

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/errdefs"
)

// TestSetContainerRemoving tests setContainerRemoving sets removing
// state correctly.
func TestSetContainerRemoving(t *testing.T) {
	testID := "test-id"
	for _, test := range []struct {
		desc      string
		status    containerstore.Status
		expectErr bool
	}{
		{
			desc: "should return error when container is in running state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in starting state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				Starting:  true,
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in removing state",
			status: containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
				Removing:   true,
			},
			expectErr: true,
		},
		{
			desc: "should not return error when container is not running and removing",
			status: containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			expectErr: false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: testID},
				containerstore.WithFakeStatus(test.status),
			)
			assert.NoError(t, err)
			err = setContainerRemoving(container)
			if test.expectErr {
				assert.Error(t, err)
				assert.Equal(t, test.status, container.Status.Get(), "metadata should not be updated")
			} else {
				assert.NoError(t, err)
				assert.True(t, container.Status.Get().Removing, "removing should be set")
				assert.NoError(t, resetContainerRemoving(container))
				assert.False(t, container.Status.Get().Removing, "removing should be reset")
			}
		})
	}
}

type removeCheckpointContainer struct {
	containerd.Container
	infoErr   error
	delete    func()
	deleteErr error
}

func (c *removeCheckpointContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return containers.Container{}, c.infoErr
}

func (c *removeCheckpointContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	c.delete()
	return c.deleteErr
}

type removeCheckpointImageStore struct {
	images.Store
	delete        func(string) error
	deleteOptions []images.DeleteOptions
}

func (s *removeCheckpointImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	var options images.DeleteOptions
	for _, opt := range opts {
		if err := opt(ctx, &options); err != nil {
			return err
		}
	}
	s.deleteOptions = append(s.deleteOptions, options)
	return s.delete(name)
}

func TestRemoveContainerTaskCheckpoint(t *testing.T) {
	const (
		containerID   = "test-container"
		containerName = "test-container-name"
		imageName     = "localhost/checkpoint:test"
	)
	deleteFailure := errors.New("image deletion failed")
	for _, test := range []struct {
		name           string
		containerGone  bool
		firstDeleteErr error
	}{
		{name: "retry failed image deletion", firstDeleteErr: deleteFailure},
		{name: "retry after container metadata disappeared", containerGone: true, firstDeleteErr: deleteFailure},
		{name: "image already deleted", firstDeleteErr: errdefs.ErrNotFound},
		{name: "container metadata and image already deleted", containerGone: true, firstDeleteErr: errdefs.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestCRIService()
			c.config.RootDir = t.TempDir()
			c.config.StateDir = t.TempDir()
			c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})
			t.Cleanup(c.containerEventsQ.Shutdown)

			var calls []string
			backend := &removeCheckpointContainer{delete: func() {
				calls = append(calls, "container")
			}}
			if test.containerGone {
				backend.infoErr = errdefs.ErrNotFound
			}
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: containerID},
				containerstore.WithContainer(backend),
				containerstore.WithFakeStatus(containerstore.Status{
					CreatedAt:           1,
					TaskCheckpointImage: imageName,
				}),
			)
			require.NoError(t, err)
			require.NoError(t, c.containerStore.Add(container))
			require.NoError(t, c.containerNameIndex.Reserve(containerName, containerID))

			imageStore := &removeCheckpointImageStore{delete: func(name string) error {
				assert.Equal(t, imageName, name)
				if !test.containerGone {
					assert.True(t, container.Status.Get().Removing, "image cleanup must hold the removal lock")
				}
				calls = append(calls, "image")
				if len(calls) == 1 {
					return test.firstDeleteErr
				}
				return nil
			}}
			c.client, err = containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, c.client.Close()) })

			request := &runtime.RemoveContainerRequest{ContainerId: containerID}
			_, err = c.RemoveContainer(t.Context(), request)
			require.Len(t, imageStore.deleteOptions, 1)
			assert.True(t, imageStore.deleteOptions[0].Synchronous)
			if errors.Is(test.firstDeleteErr, deleteFailure) {
				require.ErrorIs(t, err, deleteFailure)
				require.Equal(t, []string{"image"}, calls, "container metadata must survive image cleanup failure")
				_, err := c.containerStore.Get(containerID)
				require.NoError(t, err, "CRI must retain checkpoint ownership for retry")
				assert.False(t, container.Status.Get().Removing)
				require.Error(t, c.containerNameIndex.Reserve(containerName, "other-container"))

				_, err = c.RemoveContainer(t.Context(), request)
				require.NoError(t, err)
			} else {
				require.NoError(t, err)
			}

			wantCalls := []string{"image"}
			if errors.Is(test.firstDeleteErr, deleteFailure) {
				wantCalls = append(wantCalls, "image")
			}
			if !test.containerGone {
				wantCalls = append(wantCalls, "container")
			}
			assert.Equal(t, wantCalls, calls)
			_, err = c.containerStore.Get(containerID)
			require.ErrorIs(t, err, errdefs.ErrNotFound)
			require.NoError(t, c.containerNameIndex.Reserve(containerName, "other-container"))
		})
	}
}

func TestRemoveContainerTaskCheckpointStatusFailure(t *testing.T) {
	const (
		containerID = "test-container"
		imageName   = "localhost/checkpoint:test"
	)
	for _, containerGone := range []bool{false, true} {
		name := "container metadata exists"
		if containerGone {
			name = "container metadata disappeared"
		}
		t.Run(name, func(t *testing.T) {
			c := newTestCRIService()
			c.config.RootDir = t.TempDir()
			c.config.StateDir = t.TempDir()
			c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})
			t.Cleanup(c.containerEventsQ.Shutdown)

			var calls []string
			backend := &removeCheckpointContainer{delete: func() { calls = append(calls, "container") }}
			if containerGone {
				backend.infoErr = errdefs.ErrNotFound
			}
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: containerID},
				containerstore.WithContainer(backend),
				containerstore.WithFakeStatus(containerstore.Status{
					CreatedAt:           1,
					StartedAt:           2,
					FinishedAt:          3,
					TaskCheckpointImage: imageName,
				}),
			)
			require.NoError(t, err)
			syncFailure := errors.New("status checkpoint failed")
			status := &checkpointSyncFailureStatus{StatusStorage: container.Status, err: syncFailure}
			container.Status = status
			require.NoError(t, c.containerStore.Add(container))

			imageStore := &removeCheckpointImageStore{delete: func(name string) error {
				assert.Equal(t, imageName, name)
				calls = append(calls, "image")
				if len(calls) == 1 {
					return nil
				}
				return errdefs.ErrNotFound
			}}
			c.client, err = containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, c.client.Close()) })

			request := &runtime.RemoveContainerRequest{ContainerId: containerID}
			_, err = c.RemoveContainer(t.Context(), request)
			require.ErrorIs(t, err, syncFailure)
			assert.Equal(t, []string{"image"}, calls)
			_, err = c.containerStore.Get(containerID)
			require.NoError(t, err, "retain the container when cleanup status cannot be saved")
			assert.Equal(t, imageName, status.Get().TaskCheckpointImage)
			assert.False(t, status.Get().Removing)

			status.err = nil
			_, err = c.RemoveContainer(t.Context(), request)
			require.NoError(t, err)
			assert.Empty(t, status.Get().TaskCheckpointImage)
			wantCalls := []string{"image", "image"}
			if !containerGone {
				wantCalls = append(wantCalls, "container")
			}
			assert.Equal(t, wantCalls, calls)
			_, err = c.containerStore.Get(containerID)
			require.ErrorIs(t, err, errdefs.ErrNotFound)
		})
	}
}

func TestRemoveContainerTaskCheckpointPendingRestore(t *testing.T) {
	const (
		containerID = "test-container"
		imageName   = "localhost/checkpoint:test"
	)
	c := newTestCRIService()
	deleteFailure := errors.New("container deletion failed")
	backend := &removeCheckpointContainer{delete: func() {}, deleteErr: deleteFailure}
	container, err := containerstore.NewContainer(
		containerstore.Metadata{ID: containerID},
		containerstore.WithContainer(backend),
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt:           1,
			TaskCheckpointImage: imageName,
		}),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(container))
	imageStore := &removeCheckpointImageStore{delete: func(name string) error {
		assert.Equal(t, imageName, name)
		return nil
	}}
	c.client, err = containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.client.Close()) })

	_, err = c.RemoveContainer(t.Context(), &runtime.RemoveContainerRequest{ContainerId: containerID})
	require.ErrorIs(t, err, deleteFailure)
	_, err = c.containerStore.Get(containerID)
	require.NoError(t, err)
	assert.Equal(t, imageName, container.Status.Get().TaskCheckpointImage, "a failed removal must not turn a restored container into an ordinary container")
	assert.False(t, container.Status.Get().Removing)
}

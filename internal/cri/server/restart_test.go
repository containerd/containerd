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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
)

type checkpointRecoveryContainer struct {
	containerd.Container
	metadata containerstore.Metadata
	task     containerd.Task
}

func (c *checkpointRecoveryContainer) ID() string {
	return c.metadata.ID
}

func (c *checkpointRecoveryContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	metadata, err := typeurl.MarshalAny(&c.metadata)
	if err != nil {
		return nil, err
	}
	return map[string]typeurl.Any{crilabels.ContainerMetadataExtension: metadata}, nil
}

func (c *checkpointRecoveryContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	if c.task != nil {
		return c.task, nil
	}
	return nil, errdefs.ErrNotFound
}

type checkpointRecoveryTask struct {
	containerd.Task
}

func (t *checkpointRecoveryTask) Status(context.Context) (containerd.Status, error) {
	return containerd.Status{Status: containerd.Running}, nil
}

func (t *checkpointRecoveryTask) Wait(context.Context) (<-chan containerd.ExitStatus, error) {
	return nil, nil
}

func (t *checkpointRecoveryTask) Pid() uint32 {
	return 42
}

func TestLoadContainerTaskCheckpoint(t *testing.T) {
	const imageName = "localhost/checkpoint:test"
	deleteFailure := errors.New("image deletion failed")
	for _, test := range []struct {
		name        string
		startedAt   int64
		finishedAt  int64
		taskRunning bool
		deleteErr   error
		wantImage   string
	}{
		{name: "pending restore", wantImage: imageName},
		{name: "started before timestamp saved", taskRunning: true},
		{name: "started task", startedAt: 2},
		{name: "exited task", startedAt: 2, finishedAt: 3},
		{name: "failed start", finishedAt: 3},
		{name: "image already deleted", startedAt: 2, deleteErr: errdefs.ErrNotFound},
		{name: "failed image deletion", startedAt: 2, deleteErr: deleteFailure, wantImage: imageName},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestCRIService()
			c.config.RootDir = t.TempDir()
			c.config.StateDir = t.TempDir()
			backend := &checkpointRecoveryContainer{metadata: containerstore.Metadata{
				ID:        "test-container",
				SandboxID: "test-sandbox",
				Config:    &runtime.ContainerConfig{},
			}}
			if test.taskRunning {
				backend.task = &checkpointRecoveryTask{}
			}
			require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
				sandboxstore.Metadata{ID: backend.metadata.SandboxID, Config: &runtime.PodSandboxConfig{}},
				sandboxstore.Status{State: sandboxstore.StateReady},
			)))
			root := c.getContainerRootDir(backend.ID())
			require.NoError(t, os.MkdirAll(root, 0700))
			_, err := containerstore.StoreStatus(root, backend.ID(), containerstore.Status{
				CreatedAt:           1,
				StartedAt:           test.startedAt,
				FinishedAt:          test.finishedAt,
				TaskCheckpointImage: imageName,
			})
			require.NoError(t, err)

			deleteCalls := 0
			imageStore := &removeCheckpointImageStore{delete: func(name string) error {
				deleteCalls++
				assert.Equal(t, imageName, name)
				status, err := containerstore.LoadStatus(root, backend.ID())
				require.NoError(t, err)
				assert.NotEqual(t, runtime.ContainerState_CONTAINER_CREATED, status.State(), "save the final task state before deleting the image")
				return test.deleteErr
			}}
			c.client, err = containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, c.client.Close()) })

			container, exitCh, _, err := c.loadContainer(t.Context(), backend)
			require.NoError(t, err)
			assert.Nil(t, exitCh)
			if container.IO != nil {
				t.Cleanup(func() { require.NoError(t, container.IO.Close()) })
			}
			assert.Equal(t, test.wantImage, container.Status.Get().TaskCheckpointImage)
			status, err := containerstore.LoadStatus(root, backend.ID())
			require.NoError(t, err)
			assert.Equal(t, test.wantImage, status.TaskCheckpointImage)
			if test.startedAt == 0 && test.finishedAt == 0 && !test.taskRunning {
				assert.Zero(t, deleteCalls, "a CREATED container still needs its checkpoint image")
			} else {
				assert.Equal(t, 1, deleteCalls)
			}
		})
	}
}

type checkpointSyncFailureStatus struct {
	containerstore.StatusStorage
	err error
}

func (s *checkpointSyncFailureStatus) UpdateSync(update containerstore.UpdateFunc) error {
	if s.err != nil {
		return s.err
	}
	return s.StatusStorage.UpdateSync(update)
}

func TestLoadContainerTaskCheckpointAfterStatusFailure(t *testing.T) {
	const imageName = "localhost/checkpoint:test"
	c := newTestCRIService()
	c.config.RootDir = t.TempDir()
	backend := &checkpointRecoveryContainer{metadata: containerstore.Metadata{ID: "test-container"}}
	root := c.getContainerRootDir(backend.ID())
	require.NoError(t, os.MkdirAll(root, 0700))
	container, err := containerstore.NewContainer(backend.metadata,
		containerstore.WithStatus(containerstore.Status{
			CreatedAt:           1,
			StartedAt:           2,
			TaskCheckpointImage: imageName,
		}, root),
	)
	require.NoError(t, err)
	syncFailure := errors.New("status checkpoint failed")
	container.Status = &checkpointSyncFailureStatus{StatusStorage: container.Status, err: syncFailure}

	deleteCalls := 0
	imageStore := &removeCheckpointImageStore{delete: func(name string) error {
		deleteCalls++
		assert.Equal(t, imageName, name)
		if deleteCalls == 1 {
			return nil
		}
		return errdefs.ErrNotFound
	}}
	c.client, err = containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.client.Close()) })

	require.ErrorIs(t, c.removeContainerTaskCheckpoint(t.Context(), container), syncFailure)
	assert.Equal(t, imageName, container.Status.Get().TaskCheckpointImage)
	status, err := containerstore.LoadStatus(root, backend.ID())
	require.NoError(t, err)
	assert.Equal(t, imageName, status.TaskCheckpointImage, "keep cleanup work across restart")

	recovered, _, _, err := c.loadContainer(t.Context(), backend)
	require.NoError(t, err)
	assert.Empty(t, recovered.Status.Get().TaskCheckpointImage)
	assert.Equal(t, 2, deleteCalls, "retry deletion and accept an already deleted image")
	status, err = containerstore.LoadStatus(root, backend.ID())
	require.NoError(t, err)
	assert.Empty(t, status.TaskCheckpointImage)
}

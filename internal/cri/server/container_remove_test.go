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
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/runtime-spec/specs-go"
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

func TestRemoveContainerSnapshotCleanupFallback(t *testing.T) {
	for _, test := range []struct {
		name              string
		deleteErrs        []error
		wantErr           string
		wantDeleteOpts    []int
		wantContainerGone bool
	}{
		{
			name:              "busy snapshot cleanup falls back to metadata delete",
			deleteErrs:        []error{fmt.Errorf("remove snapshot: %w", syscall.EBUSY)},
			wantDeleteOpts:    []int{1, 0},
			wantContainerGone: true,
		},
		{
			name:              "busy snapshot cleanup (gRPC device or resource busy string error) falls back to metadata delete",
			deleteErrs:        []error{errors.New("rpc error: code = Unknown desc = failed to remove snapshot: device or resource busy")},
			wantDeleteOpts:    []int{1, 0},
			wantContainerGone: true,
		},
		{
			name:              "busy snapshot cleanup (gRPC EBUSY string error) falls back to metadata delete",
			deleteErrs:        []error{errors.New("rpc error: code = Internal desc = EBUSY: some other info")},
			wantDeleteOpts:    []int{1, 0},
			wantContainerGone: true,
		},
		{
			name:              "busy snapshot cleanup (gRPC resource busy string error) falls back to metadata delete",
			deleteErrs:        []error{errors.New("rpc error: code = Unknown desc = remove snapshot: resource busy")},
			wantDeleteOpts:    []int{1, 0},
			wantContainerGone: true,
		},
		{
			name:           "busy snapshot cleanup returns fallback error",
			deleteErrs:     []error{fmt.Errorf("remove snapshot: %w", syscall.EBUSY), errors.New("metadata delete failed")},
			wantErr:        "failed to delete containerd container",
			wantDeleteOpts: []int{1, 0},
		},
		{
			name:           "non busy snapshot cleanup error is returned",
			deleteErrs:     []error{errors.New("snapshotter configuration failed")},
			wantErr:        "snapshotter configuration failed",
			wantDeleteOpts: []int{1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestCRIService()
			c.containerEventsQ = eventq.New[runtime.ContainerEventResponse](time.Minute, func(runtime.ContainerEventResponse) {})
			fake := addRemoveContainerTestContainer(t, c, test.deleteErrs)

			_, err := c.RemoveContainer(context.Background(), &runtime.RemoveContainerRequest{ContainerId: fake.id})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.wantDeleteOpts, fake.deleteOptCounts)

			cntr, err := c.containerStore.Get(fake.id)
			if test.wantContainerGone {
				require.True(t, errdefs.IsNotFound(err), "container should be removed from the CRI store")
			} else {
				require.NoError(t, err)
				require.False(t, cntr.Status.Get().Removing, "removing status should be reset to false on failure")
			}
		})
	}
}

func addRemoveContainerTestContainer(t *testing.T, c *criService, deleteErrs []error) *fakeRemoveContainerdContainer {
	t.Helper()

	const id = "test-container-id"
	fake := &fakeRemoveContainerdContainer{
		id:         id,
		deleteErrs: append([]error(nil), deleteErrs...),
		info: containers.Container{
			ID:      id,
			Runtime: containers.RuntimeInfo{Name: "runc"},
		},
	}
	now := time.Now()
	cntr, err := containerstore.NewContainer(
		containerstore.Metadata{ID: id, SandboxID: "test-sandbox-id"},
		containerstore.WithContainer(fake),
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt:  now.Add(-2 * time.Second).UnixNano(),
			StartedAt:  now.Add(-time.Second).UnixNano(),
			FinishedAt: now.UnixNano(),
		}),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(cntr))
	return fake
}

var _ containerd.Container = (*fakeRemoveContainerdContainer)(nil)

type fakeRemoveContainerdContainer struct {
	id              string
	info            containers.Container
	deleteErrs      []error
	deleteOptCounts []int
}

func (f *fakeRemoveContainerdContainer) ID() string {
	return f.id
}

func (f *fakeRemoveContainerdContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return f.info, nil
}

func (f *fakeRemoveContainerdContainer) Delete(_ context.Context, opts ...containerd.DeleteOpts) error {
	f.deleteOptCounts = append(f.deleteOptCounts, len(opts))
	if len(f.deleteErrs) == 0 {
		return nil
	}
	err := f.deleteErrs[0]
	f.deleteErrs = f.deleteErrs[1:]
	return err
}

func (f *fakeRemoveContainerdContainer) NewTask(context.Context, cio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Spec(context.Context) (*specs.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	return nil, errdefs.ErrNotFound
}

func (f *fakeRemoveContainerdContainer) Image(context.Context) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Labels(context.Context) (map[string]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeRemoveContainerdContainer) Restore(context.Context, cio.Creator, string) (int, error) {
	return 0, errdefs.ErrNotImplemented
}

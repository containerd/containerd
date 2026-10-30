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
	"path/filepath"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type checkpointControllerStub struct {
	sandbox.Controller
	checkpoint func(context.Context, string, sandbox.CheckpointOptions) error
	restore    func(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error)
}

func (c *checkpointControllerStub) Checkpoint(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) error {
	return c.checkpoint(ctx, sandboxID, opts)
}

func (c *checkpointControllerStub) Restore(ctx context.Context, sandboxID string, opts sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return c.restore(ctx, sandboxID, opts)
}

type checkpointSandboxService struct {
	sandboxService
	controller sandbox.Controller
	sandboxer  string
	err        error
}

func (s *checkpointSandboxService) SandboxController(sandboxer string) (sandbox.Controller, error) {
	s.sandboxer = sandboxer
	return s.controller, s.err
}

func checkpointAPIContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestRestoreOptionsFromCRIContainsOnlyData(t *testing.T) {
	request := &runtime.RestorePodRequest{
		CheckpointPath: filepath.Join(t.TempDir(), "checkpoint"),
		RuntimeHandler: "vm-runtime",
		Options:        map[string]string{"controller-specific": "value"},
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "new-uid"},
		},
		ContainerConfigs: []*runtime.ContainerConfig{{
			Metadata: &runtime.ContainerMetadata{Name: "app"},
			Image:    &runtime.ImageSpec{Image: "example/app:latest"},
		}},
	}
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	assert.Equal(t, request.CheckpointPath, options.CheckpointPath)
	assert.Equal(t, request.Options, options.Options)
	require.Len(t, options.Containers, 1)
	assert.Empty(t, options.Containers[0].ID)
	assert.Equal(t, "app", options.Containers[0].Name)

	sandboxConfig := new(runtime.PodSandboxConfig)
	require.NoError(t, typeurl.UnmarshalTo(options.SandboxConfig, sandboxConfig))
	assert.True(t, proto.Equal(request.Config, sandboxConfig))
	containerConfig := new(runtime.ContainerConfig)
	require.NoError(t, typeurl.UnmarshalTo(options.Containers[0].Config, containerConfig))
	assert.True(t, proto.Equal(request.ContainerConfigs[0], containerConfig))
}

func TestDeleteTaskCheckpoint(t *testing.T) {
	const image = "localhost/checkpoint:prepared"
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "deletion fails", err: errdefs.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &removeCheckpointImageStore{delete: func(name string) error {
				assert.Equal(t, image, name)
				return test.err
			}}
			cri := newTestCRIService()
			client, err := containerd.New("", containerd.WithServices(containerd.WithImageStore(store)))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			cri.client = client

			require.ErrorIs(t, cri.deleteTaskCheckpoint(t.Context(), image), test.err)
			require.Len(t, store.deleteOptions, 1)
			assert.True(t, store.deleteOptions[0].Synchronous)
		})
	}
}

func TestValidateRestoreResult(t *testing.T) {
	requested := []sandbox.RestoreContainer{
		{ID: "one", Name: "app"},
		{ID: "two", Name: "sidecar"},
	}
	prepared, err := validateRestoreResult(requested, sandbox.RestoreResult{
		RestoredContainers: []sandbox.RestoredContainer{
			{ID: "two", Name: "sidecar"},
			{ID: "one", Name: "app", TaskCheckpointImage: "localhost/checkpoint:app"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"app":     "localhost/checkpoint:app",
		"sidecar": "",
	}, prepared)

	for _, test := range []struct {
		name       string
		containers []sandbox.RestoredContainer
		wantErr    string
	}{
		{
			name:       "missing container",
			containers: []sandbox.RestoredContainer{{ID: "one", Name: "app"}},
			wantErr:    "prepared 1 containers, expected 2",
		},
		{
			name: "empty name",
			containers: []sandbox.RestoredContainer{
				{ID: "one"},
				{ID: "two", Name: "sidecar"},
			},
			wantErr: "has no container name",
		},
		{
			name: "duplicate name",
			containers: []sandbox.RestoredContainer{
				{ID: "one", Name: "app"},
				{ID: "one", Name: "app"},
			},
			wantErr: "more than once",
		},
		{
			name: "unexpected name",
			containers: []sandbox.RestoredContainer{
				{ID: "one", Name: "app"},
				{ID: "two", Name: "unexpected"},
			},
			wantErr: "unexpected container",
		},
		{
			name: "empty ID",
			containers: []sandbox.RestoredContainer{
				{Name: "app"},
				{ID: "two", Name: "sidecar"},
			},
			wantErr: "has no container ID",
		},
		{
			name: "incorrect ID",
			containers: []sandbox.RestoredContainer{
				{ID: "other", Name: "app"},
				{ID: "two", Name: "sidecar"},
			},
			wantErr: `with ID "other", expected "one"`,
		},
		{
			name: "duplicate ID",
			containers: []sandbox.RestoredContainer{
				{ID: "one", Name: "app"},
				{ID: "one", Name: "sidecar"},
			},
			wantErr: `with ID "one", expected "two"`,
		},
		{
			name: "swapped IDs",
			containers: []sandbox.RestoredContainer{
				{ID: "two", Name: "app"},
				{ID: "one", Name: "sidecar"},
			},
			wantErr: `with ID "two", expected "one"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := validateRestoreResult(requested, sandbox.RestoreResult{RestoredContainers: test.containers})
			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, prepared)
		})
	}
}

type restoreOperationsStub struct {
	calls        []string
	containerIDs []string
	createCalls  int
	createErrAt  int
	saveErr      error
	commitErr    error
	saved        map[string]string
	deleted      []string
}

func (o *restoreOperationsStub) runPodSandboxForRestore(_ context.Context, request *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	o.calls = append(o.calls, "run:"+request.GetRuntimeHandler())
	return &runtime.RunPodSandboxResponse{PodSandboxId: "new-sandbox"}, nil
}

func (o *restoreOperationsStub) commitPodRestore(_ context.Context, id string) error {
	o.calls = append(o.calls, "commit:"+id)
	return o.commitErr
}

func (o *restoreOperationsStub) createContainerForRestore(_ context.Context, request *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	o.createCalls++
	name := request.GetConfig().GetMetadata().GetName()
	o.calls = append(o.calls, "create:"+name)
	if o.createErrAt == o.createCalls {
		return nil, errors.New("create failed")
	}
	return &runtime.CreateContainerResponse{ContainerId: o.containerIDs[o.createCalls-1]}, nil
}

func (o *restoreOperationsStub) RemovePodSandbox(_ context.Context, request *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	o.calls = append(o.calls, "remove:"+request.GetPodSandboxId())
	return &runtime.RemovePodSandboxResponse{}, nil
}

func (o *restoreOperationsStub) saveContainerTaskCheckpoint(containerID, image string) error {
	o.calls = append(o.calls, "save:"+containerID)
	if o.saveErr != nil {
		return o.saveErr
	}
	if o.saved == nil {
		o.saved = make(map[string]string)
	}
	o.saved[containerID] = image
	return nil
}

func (o *restoreOperationsStub) deleteTaskCheckpoint(_ context.Context, image string) error {
	o.calls = append(o.calls, "delete:"+image)
	o.deleted = append(o.deleted, image)
	return nil
}

func TestRestorePodResourcesKeepsSequencingInCRI(t *testing.T) {
	request := restoreTransactionRequest(t)
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{containerIDs: []string{"new-app", "new-sidecar"}}
	controller := &checkpointControllerStub{
		restore: func(_ context.Context, sandboxID string, options sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			operations.calls = append(operations.calls, "controller:"+sandboxID)
			assert.Equal(t, "new-app", options.Containers[0].ID)
			assert.Equal(t, "new-sidecar", options.Containers[1].ID)
			return sandbox.RestoreResult{
				RestoredContainers: []sandbox.RestoredContainer{
					{ID: "new-app", Name: "app", TaskCheckpointImage: "localhost/checkpoint:app"},
					{ID: "new-sidecar", Name: "sidecar"},
				},
			}, nil
		},
	}

	response, err := restorePodResources(context.Background(), request, options, controller, operations)
	require.NoError(t, err)
	assert.Equal(t, "new-sandbox", response.GetPodSandboxId())
	require.Len(t, response.GetRestoredContainers(), 2)
	assert.Equal(t, "new-app", response.GetRestoredContainers()[0].GetContainerId())
	assert.Equal(t, map[string]string{"new-app": "localhost/checkpoint:app", "new-sidecar": ""}, operations.saved)
	assert.Equal(t, []string{
		"run:vm-runtime",
		"create:app",
		"create:sidecar",
		"controller:new-sandbox",
		"save:new-app",
		"save:new-sidecar",
		"commit:new-sandbox",
	}, operations.calls)
}

func TestRestorePodResourcesRollsBackRejectedOptions(t *testing.T) {
	request := restoreTransactionRequest(t)
	request.Options = map[string]string{"unsupported-option": "value"}
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{containerIDs: []string{"new-app", "new-sidecar"}}
	controller := &checkpointControllerStub{
		restore: func(_ context.Context, sandboxID string, opts sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			operations.calls = append(operations.calls, "controller:"+sandboxID)
			require.Equal(t, request.Options, opts.Options)
			return sandbox.RestoreResult{}, fmt.Errorf("unsupported restore options: %w", errdefs.ErrInvalidArgument)
		},
	}

	response, err := restorePodResources(checkpointAPIContext(t), request, options, controller, operations)
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	assert.Nil(t, response)
	assert.Empty(t, operations.saved)
	assert.Equal(t, []string{
		"run:vm-runtime",
		"create:app",
		"create:sidecar",
		"controller:new-sandbox",
		"remove:new-sandbox",
	}, operations.calls)
}

func TestRestorePodResourcesRollsBackControllerArtifacts(t *testing.T) {
	request := restoreTransactionRequest(t)
	request.ContainerConfigs = request.ContainerConfigs[:1]
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{containerIDs: []string{"new-app"}}
	controller := &checkpointControllerStub{
		restore: func(_ context.Context, _ string, _ sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			return sandbox.RestoreResult{
				RestoredContainers: []sandbox.RestoredContainer{{
					Name:                "unexpected",
					TaskCheckpointImage: "localhost/checkpoint:prepared",
				}},
			}, nil
		},
	}

	_, err = restorePodResources(context.Background(), request, options, controller, operations)
	require.ErrorContains(t, err, "unexpected container")
	assert.Equal(t, []string{
		"run:vm-runtime",
		"create:app",
		"delete:localhost/checkpoint:prepared",
		"remove:new-sandbox",
	}, operations.calls)
}

func TestRestorePodResourcesRollsBackMismatchedContainerIDs(t *testing.T) {
	for _, test := range []struct {
		name string
		ids  []string
	}{
		{name: "empty ID", ids: []string{"", "new-sidecar"}},
		{name: "incorrect ID", ids: []string{"other", "new-sidecar"}},
		{name: "duplicate ID", ids: []string{"new-app", "new-app"}},
		{name: "swapped IDs", ids: []string{"new-sidecar", "new-app"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := restoreTransactionRequest(t)
			options, err := restoreOptionsFromCRI(request)
			require.NoError(t, err)
			operations := &restoreOperationsStub{containerIDs: []string{"new-app", "new-sidecar"}}
			controller := &checkpointControllerStub{
				restore: func(_ context.Context, _ string, _ sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
					return sandbox.RestoreResult{
						RestoredContainers: []sandbox.RestoredContainer{
							{ID: test.ids[0], Name: "app", TaskCheckpointImage: "localhost/checkpoint:app"},
							{ID: test.ids[1], Name: "sidecar", TaskCheckpointImage: "localhost/checkpoint:sidecar"},
						},
					}, nil
				},
			}

			response, err := restorePodResources(context.Background(), request, options, controller, operations)
			require.ErrorContains(t, err, "sandbox controller")
			assert.Nil(t, response)
			assert.Empty(t, operations.saved)
			assert.Equal(t, []string{
				"run:vm-runtime",
				"create:app",
				"create:sidecar",
				"delete:localhost/checkpoint:app",
				"delete:localhost/checkpoint:sidecar",
				"remove:new-sandbox",
			}, operations.calls)
		})
	}
}

func TestRestorePodResourcesCleansTransferredTaskCheckpoint(t *testing.T) {
	request := restoreTransactionRequest(t)
	request.ContainerConfigs = request.ContainerConfigs[:1]
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{
		containerIDs: []string{"new-app"},
		saveErr:      errors.New("save failed"),
	}
	controller := &checkpointControllerStub{
		restore: func(_ context.Context, _ string, _ sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			return sandbox.RestoreResult{
				RestoredContainers: []sandbox.RestoredContainer{{
					ID:                  "new-app",
					Name:                "app",
					TaskCheckpointImage: "localhost/checkpoint:prepared",
				}},
			}, nil
		},
	}

	_, err = restorePodResources(context.Background(), request, options, controller, operations)
	require.ErrorContains(t, err, "save failed")
	assert.Equal(t, []string{
		"run:vm-runtime",
		"create:app",
		"save:new-app",
		"delete:localhost/checkpoint:prepared",
		"remove:new-sandbox",
	}, operations.calls)
}

func TestRestorePodResourcesRollsBackCreateFailure(t *testing.T) {
	request := restoreTransactionRequest(t)
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{
		containerIDs: []string{"new-app", "new-sidecar"},
		createErrAt:  2,
	}
	controller := &checkpointControllerStub{
		restore: func(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			return sandbox.RestoreResult{}, fmt.Errorf("controller must not be called")
		},
	}

	_, err = restorePodResources(context.Background(), request, options, controller, operations)
	require.ErrorContains(t, err, "create failed")
	assert.Equal(t, []string{
		"run:vm-runtime",
		"create:app",
		"create:sidecar",
		"remove:new-sandbox",
	}, operations.calls)
}

func restoreTransactionRequest(t *testing.T) *runtime.RestorePodRequest {
	return &runtime.RestorePodRequest{
		CheckpointPath: filepath.Join(t.TempDir(), "checkpoint"),
		RuntimeHandler: "vm-runtime",
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "new-uid"},
		},
		ContainerConfigs: []*runtime.ContainerConfig{
			{
				Metadata: &runtime.ContainerMetadata{Name: "app"},
				Image:    &runtime.ImageSpec{Image: "example/app:latest"},
			},
			{
				Metadata: &runtime.ContainerMetadata{Name: "sidecar"},
				Image:    &runtime.ImageSpec{Image: "example/sidecar:latest"},
			},
		},
	}
}

func TestPodCheckpointRequestsRequireDeadline(t *testing.T) {
	err := requirePodCheckpointDeadline(context.Background(), "pod checkpoint")
	require.ErrorContains(t, err, "finite context deadline")
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, deadlineCancel := context.WithTimeout(ctx, time.Second)
	defer deadlineCancel()
	require.ErrorIs(t, requirePodCheckpointDeadline(ctx, "pod checkpoint"), context.Canceled)
}

func TestCheckpointControllerCapability(t *testing.T) {
	cri := newTestCRIService()
	service := &checkpointSandboxService{controller: &fakeSandboxController{}}
	cri.sandboxService = service

	_, err := cri.checkpointController("vm-runtime", "restore")
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)
	assert.Equal(t, "vm-runtime", service.sandboxer)
}

func TestReserveContainerCheckpoints(t *testing.T) {
	cri := newTestCRIService()
	release, err := cri.reserveContainerCheckpoints([]string{"container-a", "container-b"})
	require.NoError(t, err)

	_, err = cri.reserveContainerCheckpoints([]string{"container-b"})
	require.ErrorContains(t, err, "already in progress")

	release()
	releaseAgain, err := cri.reserveContainerCheckpoints([]string{"container-b"})
	require.NoError(t, err)
	releaseAgain()
}

type restoreImageService struct {
	fakeImageService
	present map[string]bool
	err     error
}

func (s *restoreImageService) LocalResolve(ref string) (imagestore.Image, error) {
	if s.err != nil {
		return imagestore.Image{}, s.err
	}
	if !s.present[ref] {
		return imagestore.Image{}, errdefs.ErrNotFound
	}
	return imagestore.Image{ID: ref}, nil
}

func TestRequireRestoreImages(t *testing.T) {
	request := &runtime.RestorePodRequest{ContainerConfigs: []*runtime.ContainerConfig{
		{Image: &runtime.ImageSpec{Image: "registry.example/app:1"}},
		{Image: &runtime.ImageSpec{Image: "registry.example/sidecar:1"}},
	}}
	for _, test := range []struct {
		name    string
		images  *restoreImageService
		wantErr error
	}{
		{name: "all present", images: &restoreImageService{present: map[string]bool{
			"registry.example/app:1": true, "registry.example/sidecar:1": true,
		}}},
		{name: "missing image is not pulled", images: &restoreImageService{present: map[string]bool{
			"registry.example/app:1": true,
		}}, wantErr: errdefs.ErrFailedPrecondition},
		{name: "lookup failure", images: &restoreImageService{err: errdefs.ErrUnavailable}, wantErr: errdefs.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			cri := newTestCRIService()
			cri.ImageService = test.images
			err := cri.requireRestoreImages(request)
			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestRestorePodResourcesRollsBackCommitFailure(t *testing.T) {
	request := restoreTransactionRequest(t)
	request.ContainerConfigs = request.ContainerConfigs[:1]
	options, err := restoreOptionsFromCRI(request)
	require.NoError(t, err)
	operations := &restoreOperationsStub{containerIDs: []string{"new-app"}, commitErr: errors.New("injected commit failure")}
	controller := &checkpointControllerStub{
		restore: func(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
			return sandbox.RestoreResult{RestoredContainers: []sandbox.RestoredContainer{{ID: "new-app", Name: "app", TaskCheckpointImage: "prepared-image"}}}, nil
		},
	}
	response, err := restorePodResources(t.Context(), request, options, controller, operations)
	require.ErrorContains(t, err, "injected commit failure")
	assert.Nil(t, response)
	assert.Equal(t, []string{"run:vm-runtime", "create:app", "save:new-app", "commit:new-sandbox", "delete:prepared-image", "remove:new-sandbox"}, operations.calls)
}

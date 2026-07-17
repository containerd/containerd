//go:build linux

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
	"fmt"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/sandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// The CRI entry points reach the sandbox controller only on Linux; other
// platforms report the Pod checkpoint APIs as unimplemented.

func TestCheckpointPodPassesDataToSelectedController(t *testing.T) {
	var gotID string
	var gotOptions sandbox.CheckpointOptions
	controller := &checkpointControllerStub{
		checkpoint: func(_ context.Context, id string, options sandbox.CheckpointOptions) error {
			gotID = id
			gotOptions = options
			return nil
		},
	}
	cri := newTestCRIService()
	sandboxService := &checkpointSandboxService{controller: controller}
	cri.sandboxService = sandboxService

	sandboxConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "uid"},
		Linux:    &runtime.LinuxPodSandboxConfig{CgroupParent: "/pod"},
	}
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: "sandbox-1", Config: sandboxConfig},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	sb.Sandboxer = "custom-controller"
	require.NoError(t, cri.sandboxStore.Add(sb))

	containerConfig := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{Name: "app"},
		Image:    &runtime.ImageSpec{Image: "example/app:latest"},
	}
	cntr, err := containerstore.NewContainer(
		containerstore.Metadata{
			ID:        "container-1",
			SandboxID: sb.ID,
			Config:    containerConfig,
			ImageRef:  "sha256:image",
		},
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt: 1,
			StartedAt: 2,
		}),
	)
	require.NoError(t, err)
	require.NoError(t, cri.containerStore.Add(cntr))

	for _, outputPath := range []string{"", ".", "checkpoint", "../checkpoint"} {
		t.Run(fmt.Sprintf("invalid output path %q", outputPath), func(t *testing.T) {
			_, err := cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{
				PodSandboxId: sb.ID,
				OutputPath:   outputPath,
				ContainerIds: []string{cntr.ID},
			})
			require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
			assert.Empty(t, gotID, "reject invalid paths before controller dispatch")
		})
	}

	outputPath := filepath.Join(t.TempDir(), "controller-owned", "output")
	_, err = cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: sb.ID,
		OutputPath:   outputPath,
		ContainerIds: []string{cntr.ID},
		Options:      map[string]string{"controller-specific": "value"},
	})
	require.NoError(t, err)
	assert.Equal(t, sb.ID, gotID)
	assert.Equal(t, "custom-controller", sandboxService.sandboxer)
	assert.Equal(t, outputPath, gotOptions.OutputPath)
	assert.Equal(t, map[string]string{"controller-specific": "value"}, gotOptions.Options)

	decodedSandbox := new(runtime.PodSandboxConfig)
	require.NoError(t, typeurl.UnmarshalTo(gotOptions.SandboxConfig, decodedSandbox))
	assert.True(t, proto.Equal(sandboxConfig, decodedSandbox))
	require.Len(t, gotOptions.Containers, 1)
	assert.Equal(t, cntr.ID, gotOptions.Containers[0].ID)
	assert.Equal(t, "app", gotOptions.Containers[0].Name)

	decodedConfig := new(runtime.ContainerConfig)
	require.NoError(t, typeurl.UnmarshalTo(gotOptions.Containers[0].Config, decodedConfig))
	assert.True(t, proto.Equal(containerConfig, decodedConfig))
	decodedStatus := new(runtime.ContainerStatus)
	require.NoError(t, typeurl.UnmarshalTo(gotOptions.Containers[0].Status, decodedStatus))
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, decodedStatus.State)
	assert.Equal(t, "sha256:image", decodedStatus.ImageRef)
}

func TestCheckpointPodDoesNotInterpretControllerOptions(t *testing.T) {
	controllerErr := errdefs.ErrInvalidArgument
	controller := &checkpointControllerStub{
		checkpoint: func(_ context.Context, _ string, options sandbox.CheckpointOptions) error {
			assert.Equal(t, map[string]string{"unknown-to-cri": "value"}, options.Options)
			return controllerErr
		},
	}
	cri := newTestCRIService()
	cri.sandboxService = &checkpointSandboxService{controller: controller}
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Config: &runtime.PodSandboxConfig{},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	sb.Sandboxer = "custom"
	require.NoError(t, cri.sandboxStore.Add(sb))
	cntr, err := containerstore.NewContainer(
		containerstore.Metadata{
			ID:        "container-1",
			SandboxID: sb.ID,
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "app"},
			},
		},
		containerstore.WithFakeStatus(containerstore.Status{CreatedAt: 1, StartedAt: 2}),
	)
	require.NoError(t, err)
	require.NoError(t, cri.containerStore.Add(cntr))

	_, err = cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: sb.ID,
		OutputPath:   filepath.Join(t.TempDir(), "opaque"),
		ContainerIds: []string{cntr.ID},
		Options:      map[string]string{"unknown-to-cri": "value"},
	})
	require.ErrorIs(t, err, controllerErr)
}

func TestPodCheckpointValidationErrorCodes(t *testing.T) {
	cri := newTestCRIService()

	_, err := cri.CheckpointPod(checkpointAPIContext(t), nil)
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	_, err = cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}

func TestPodCheckpointAPIsDisabledByConfiguration(t *testing.T) {
	cri := newTestCRIService()
	enabled := false
	cri.config.EnableCheckpointRestore = &enabled
	cri.shimPath = t.TempDir()

	_, err := cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{PodSandboxId: "sandbox"})
	require.ErrorContains(t, err, "checkpoint/restore support is disabled by configuration")
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
}

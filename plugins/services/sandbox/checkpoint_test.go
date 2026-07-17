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

package sandbox

import (
	"context"
	"testing"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	coresandbox "github.com/containerd/containerd/v2/core/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

type checkpointServiceController struct {
	coresandbox.Controller
	checkpointSandboxID string
	checkpointOptions   coresandbox.CheckpointOptions
	restoreSandboxID    string
	restoreOptions      coresandbox.RestoreOptions
}

func (c *checkpointServiceController) Checkpoint(_ context.Context, sandboxID string, options coresandbox.CheckpointOptions) error {
	c.checkpointSandboxID = sandboxID
	c.checkpointOptions = options
	return nil
}

func (c *checkpointServiceController) Restore(_ context.Context, sandboxID string, options coresandbox.RestoreOptions) (coresandbox.RestoreResult, error) {
	c.restoreSandboxID = sandboxID
	c.restoreOptions = options
	return coresandbox.RestoreResult{
		RestoredContainers: []coresandbox.RestoredContainer{{
			Name:                "app",
			TaskCheckpointImage: "localhost/checkpoint:app",
		}},
	}, nil
}

func TestControllerServiceTransportsCheckpointData(t *testing.T) {
	controller := new(checkpointServiceController)
	service := &controllerService{sc: map[string]coresandbox.Controller{"remote": controller}}
	sandboxConfig := &anypb.Any{TypeUrl: "types.example/Sandbox", Value: []byte("sandbox")}
	containerConfig := &anypb.Any{TypeUrl: "types.example/Container", Value: []byte("container")}
	containerStatus := &anypb.Any{TypeUrl: "types.example/Status", Value: []byte("status")}

	_, err := service.Checkpoint(context.Background(), &api.ControllerCheckpointRequest{
		Sandboxer:     "remote",
		SandboxID:     "sandbox-id",
		OutputPath:    "/checkpoint/output",
		SandboxConfig: sandboxConfig,
		Containers: []*api.ControllerCheckpointContainer{{
			ID:     "container-id",
			Name:   "app",
			Config: containerConfig,
			Status: containerStatus,
		}},
		Options: map[string]string{"mode": "controller-defined"},
	})
	require.NoError(t, err)
	assert.Equal(t, "sandbox-id", controller.checkpointSandboxID)
	assert.Equal(t, "/checkpoint/output", controller.checkpointOptions.OutputPath)
	assert.Equal(t, sandboxConfig, controller.checkpointOptions.SandboxConfig)
	require.Len(t, controller.checkpointOptions.Containers, 1)
	assert.Equal(t, containerConfig, controller.checkpointOptions.Containers[0].Config)
	assert.Equal(t, containerStatus, controller.checkpointOptions.Containers[0].Status)

	response, err := service.Restore(context.Background(), &api.ControllerRestoreRequest{
		Sandboxer:      "remote",
		SandboxID:      "new-sandbox-id",
		CheckpointPath: "/checkpoint/input",
		SandboxConfig:  sandboxConfig,
		Containers: []*api.ControllerRestoreContainer{{
			ID:     "new-container-id",
			Name:   "app",
			Config: containerConfig,
		}},
		Options: map[string]string{"mode": "controller-defined"},
	})
	require.NoError(t, err)
	assert.Equal(t, "new-sandbox-id", controller.restoreSandboxID)
	assert.Equal(t, "/checkpoint/input", controller.restoreOptions.CheckpointPath)
	assert.Equal(t, "new-container-id", controller.restoreOptions.Containers[0].ID)
	require.Len(t, response.GetContainers(), 1)
	assert.Equal(t, "localhost/checkpoint:app", response.GetContainers()[0].GetTaskCheckpointImage())
}

func TestControllerServiceRejectsMissingCheckpointCapability(t *testing.T) {
	service := &controllerService{sc: map[string]coresandbox.Controller{"plain": &stubController{}}}
	_, err := service.Checkpoint(context.Background(), &api.ControllerCheckpointRequest{Sandboxer: "plain"})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

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

package proxy

import (
	"context"
	"testing"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

type checkpointControllerClient struct {
	api.ControllerClient
	checkpointRequest *api.ControllerCheckpointRequest
	restoreRequest    *api.ControllerRestoreRequest
}

func (c *checkpointControllerClient) Checkpoint(_ context.Context, request *api.ControllerCheckpointRequest, _ ...grpc.CallOption) (*api.ControllerCheckpointResponse, error) {
	c.checkpointRequest = request
	return &api.ControllerCheckpointResponse{}, nil
}

func (c *checkpointControllerClient) Restore(_ context.Context, request *api.ControllerRestoreRequest, _ ...grpc.CallOption) (*api.ControllerRestoreResponse, error) {
	c.restoreRequest = request
	return &api.ControllerRestoreResponse{
		Containers: []*api.ControllerRestoredContainer{{
			Name:                "app",
			TaskCheckpointImage: "localhost/checkpoint:app",
		}},
	}, nil
}

func TestCheckpointControllerRoundTripsDataOverGRPC(t *testing.T) {
	client := new(checkpointControllerClient)
	controller := NewSandboxController(client, "remote", client).(sandbox.CheckpointController)
	sandboxConfig := &anypb.Any{TypeUrl: "types.example/Sandbox", Value: []byte("sandbox")}
	containerConfig := &anypb.Any{TypeUrl: "types.example/Container", Value: []byte("container")}
	containerStatus := &anypb.Any{TypeUrl: "types.example/Status", Value: []byte("status")}

	err := controller.Checkpoint(context.Background(), "sandbox-id", sandbox.CheckpointOptions{
		OutputPath:    "/checkpoint/output",
		SandboxConfig: sandboxConfig,
		Containers: []sandbox.CheckpointContainer{{
			ID:     "container-id",
			Name:   "app",
			Config: containerConfig,
			Status: containerStatus,
		}},
		Options: map[string]string{"mode": "controller-defined"},
	})
	require.NoError(t, err)
	require.NotNil(t, client.checkpointRequest)
	assert.Equal(t, "remote", client.checkpointRequest.GetSandboxer())
	assert.Equal(t, "sandbox-id", client.checkpointRequest.GetSandboxID())
	assert.Equal(t, "/checkpoint/output", client.checkpointRequest.GetOutputPath())
	assert.Equal(t, sandboxConfig, client.checkpointRequest.GetSandboxConfig())
	require.Len(t, client.checkpointRequest.GetContainers(), 1)
	assert.Equal(t, containerConfig, client.checkpointRequest.GetContainers()[0].GetConfig())
	assert.Equal(t, containerStatus, client.checkpointRequest.GetContainers()[0].GetStatus())

	result, err := controller.Restore(context.Background(), "new-sandbox-id", sandbox.RestoreOptions{
		CheckpointPath: "/checkpoint/input",
		SandboxConfig:  sandboxConfig,
		Containers: []sandbox.RestoreContainer{{
			ID:     "new-container-id",
			Name:   "app",
			Config: containerConfig,
		}},
		Options: map[string]string{"mode": "controller-defined"},
	})
	require.NoError(t, err)
	require.NotNil(t, client.restoreRequest)
	assert.Equal(t, "remote", client.restoreRequest.GetSandboxer())
	assert.Equal(t, "new-sandbox-id", client.restoreRequest.GetSandboxID())
	assert.Equal(t, "/checkpoint/input", client.restoreRequest.GetCheckpointPath())
	assert.Equal(t, "new-container-id", client.restoreRequest.GetContainers()[0].GetID())
	assert.Equal(t, containerConfig, client.restoreRequest.GetContainers()[0].GetConfig())
	require.Equal(t, sandbox.RestoreResult{
		RestoredContainers: []sandbox.RestoredContainer{{
			Name:                "app",
			TaskCheckpointImage: "localhost/checkpoint:app",
		}},
	}, result)
}

func TestCheckpointControllerIsOptionalForRemoteController(t *testing.T) {
	controller := NewSandboxController(new(checkpointControllerClient), "remote")
	_, ok := controller.(sandbox.CheckpointController)
	assert.False(t, ok)
}

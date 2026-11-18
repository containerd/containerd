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
	"time"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/containerd/containerd/v2/core/sandbox"
)

// A failing create option ends Create before the request is sent: the
// controller has no client here, so getting past the options would panic.
func TestCreateOptionError(t *testing.T) {
	c := NewSandboxController(nil, "remote")
	err := c.Create(context.Background(), sandbox.Sandbox{ID: "sb"}, sandbox.WithRuntimePath("containerd-shim-example-v1"))
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}

var restoredSpec = &anypb.Any{TypeUrl: "spec"}

// fakeControllerClient records the checkpoint and restore requests. Calling
// any other method panics on the nil embedded client.
type fakeControllerClient struct {
	api.ControllerClient
	checkpoint *api.ControllerCheckpointRequest
	restore    *api.ControllerRestoreRequest
	err        error
}

func (f *fakeControllerClient) Checkpoint(_ context.Context, req *api.ControllerCheckpointRequest, _ ...grpc.CallOption) (*api.ControllerCheckpointResponse, error) {
	f.checkpoint = req
	return &api.ControllerCheckpointResponse{}, f.err
}

func (f *fakeControllerClient) Restore(_ context.Context, req *api.ControllerRestoreRequest, _ ...grpc.CallOption) (*api.ControllerRestoreResponse, error) {
	f.restore = req
	if f.err != nil {
		return nil, f.err
	}
	return &api.ControllerRestoreResponse{
		SandboxID: req.GetSandboxID(),
		Pid:       42,
		CreatedAt: timestamppb.New(time.Unix(1, 0)),
		Labels:    map[string]string{"env": "test"},
		Address:   "ttrpc+unix:///run/sb.sock",
		Version:   3,
		Spec:      restoredSpec,
	}, nil
}

func TestCheckpoint(t *testing.T) {
	client := &fakeControllerClient{}
	c := NewSandboxController(client, "remote")

	err := c.Checkpoint(context.Background(), "sb", sandbox.CheckpointOptions{
		Path:    "/checkpoints/sb",
		Options: &anypb.Any{TypeUrl: "checkpoint-options"},
	})
	require.NoError(t, err)
	assert.Equal(t, "sb", client.checkpoint.GetSandboxID())
	assert.Equal(t, "remote", client.checkpoint.GetSandboxer())
	assert.Equal(t, "/checkpoints/sb", client.checkpoint.GetPath())
	assert.Equal(t, "checkpoint-options", client.checkpoint.GetOptions().GetTypeUrl())

	client.err = status.Error(codes.Unimplemented, "not implemented")
	err = c.Checkpoint(context.Background(), "sb", sandbox.CheckpointOptions{})
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)
}

func TestRestore(t *testing.T) {
	client := &fakeControllerClient{}
	c := NewSandboxController(client, "remote")

	inst, err := c.Restore(context.Background(), "sb", sandbox.RestoreOptions{
		Path:    "/checkpoints/sb",
		Options: &anypb.Any{TypeUrl: "restore-options"},
	})
	require.NoError(t, err)
	assert.Equal(t, "sb", client.restore.GetSandboxID())
	assert.Equal(t, "remote", client.restore.GetSandboxer())
	assert.Equal(t, "/checkpoints/sb", client.restore.GetPath())
	assert.Equal(t, "restore-options", client.restore.GetOptions().GetTypeUrl())
	assert.Equal(t, sandbox.ControllerInstance{
		SandboxID: "sb",
		Pid:       42,
		CreatedAt: time.Unix(1, 0).UTC(),
		Labels:    map[string]string{"env": "test"},
		Address:   "ttrpc+unix:///run/sb.sock",
		Version:   3,
		Spec:      restoredSpec,
	}, inst)

	client.err = status.Error(codes.Unimplemented, "not implemented")
	_, err = c.Restore(context.Background(), "sb", sandbox.RestoreOptions{})
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)
}

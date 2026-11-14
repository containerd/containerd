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

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/v2/core/sandbox"
)

// statusClient answers Status with a fixed response.
type statusClient struct {
	api.ControllerClient
	resp *api.ControllerStatusResponse
}

func (c *statusClient) Status(context.Context, *api.ControllerStatusRequest, ...grpc.CallOption) (*api.ControllerStatusResponse, error) {
	return c.resp, nil
}

// A timestamp the controller did not set is the zero time, so that callers
// can tell it from a real one.
func TestStatusTimestamps(t *testing.T) {
	createdAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	c := NewSandboxController(&statusClient{resp: &api.ControllerStatusResponse{
		CreatedAt: timestamppb.New(createdAt),
	}}, "remote")

	status, err := c.Status(context.Background(), "sb", false)
	require.NoError(t, err)
	require.Equal(t, createdAt, status.CreatedAt)
	require.True(t, status.ExitedAt.IsZero(), "got %v", status.ExitedAt)
}

// A failing create option ends Create before the request is sent: the
// controller has no client here, so getting past the options would panic.
func TestCreateOptionError(t *testing.T) {
	c := NewSandboxController(nil, "remote")
	err := c.Create(context.Background(), sandbox.Sandbox{ID: "sb"}, sandbox.WithRuntimePath("containerd-shim-example-v1"))
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}

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

package main

import (
	"context"
	"testing"

	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
)

// shuttingDown counts the Shutdown calls that reach the wrapped task service.
type shuttingDown struct {
	taskAPI.TTRPCTaskService
	calls int
}

func (s *shuttingDown) Shutdown(context.Context, *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	s.calls++
	return &ptypes.Empty{}, nil
}

func TestShutdownKeepsTheShimRunning(t *testing.T) {
	inner := &shuttingDown{}
	svc := &taskService{TTRPCTaskService: inner}

	// containerd sends Shutdown after every task deletion; the shared task
	// service would exit the shim once no container is left. The sandbox
	// shim must stay up for its pod.
	_, err := svc.Shutdown(context.Background(), &taskAPI.ShutdownRequest{ID: "container"})
	require.NoError(t, err)
	assert.Equal(t, 0, inner.calls, "the shared task service must not see the shutdown")
}

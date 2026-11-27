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
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/api/events"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types/task"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/protobuf"
)

// fakeShimTasks models the task record of one container. The record survives
// until a Delete succeeds. While an *Unavailable flag is set the matching call
// fails with Unavailable but the record is still there.
type fakeShimTasks struct {
	tasks.TasksClient
	id                string
	getUnavailable    bool
	deleteUnavailable bool
	deleted           bool
}

func (f *fakeShimTasks) Get(_ context.Context, req *tasks.GetRequest, _ ...grpc.CallOption) (*tasks.GetResponse, error) {
	if req.ContainerID != f.id || f.deleted {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	if f.getUnavailable {
		return nil, status.Error(codes.Unavailable, "shim connection unavailable")
	}
	return &tasks.GetResponse{Process: &task.Process{ID: f.id, Status: task.Status_STOPPED}}, nil
}

func (f *fakeShimTasks) Delete(_ context.Context, req *tasks.DeleteTaskRequest, _ ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	if req.ContainerID != f.id || f.deleted {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	if f.deleteUnavailable {
		return nil, status.Error(codes.Unavailable, "shim connection unavailable")
	}
	f.deleted = true
	return &tasks.DeleteResponse{}, nil
}

// A task exit must not leave the CRI container EXITED while the runtime task
// record may still exist, otherwise RemoveContainer never force-stops it and
// fails forever with "cannot delete running task".
func TestHandleContainerExitTaskUnavailable(t *testing.T) {
	const containerID, sandboxID = "test-container", "test-sandbox"

	for _, test := range []struct {
		desc string
		// deleteUnavailable makes the first cleanup attempt fail.
		deleteUnavailable bool
	}{
		{desc: "cleanup succeeds"},
		{desc: "cleanup fails then is retried", deleteUnavailable: true},
	} {
		t.Run(test.desc, func(t *testing.T) {
			now := time.Now().UnixNano()
			shim := &fakeShimTasks{id: containerID, getUnavailable: true, deleteUnavailable: test.deleteUnavailable}
			c := newTestCRIServiceWithEvents(t, shim)
			cntr := addTestContainer(t, c, containerID, sandboxID, containerstore.Status{CreatedAt: now, StartedAt: now, Pid: 42})

			exit := &eventtypes.TaskExit{ContainerID: containerID, ID: containerID, Pid: 42, ExitStatus: 1, ExitedAt: protobuf.ToTimestamp(time.Now())}

			if test.deleteUnavailable {
				// The record cannot be cleaned up yet. The handler must report
				// the cleanup failure (so it is retried) and keep the container
				// out of EXITED.
				err := c.handleContainerExit(t.Context(), exit, cntr, sandboxID)
				require.ErrorIs(t, err, errdefs.ErrUnavailable)
				require.ErrorContains(t, err, "in task-service")
				assert.False(t, shim.deleted)
				assert.NotEqual(t, runtime.ContainerState_CONTAINER_EXITED, cntr.Status.Get().State())

				// The shim recovers: the retry cleans up the record.
				shim.getUnavailable = false
				shim.deleteUnavailable = false
			}

			require.NoError(t, c.handleContainerExit(t.Context(), exit, cntr, sandboxID))
			assert.True(t, shim.deleted)
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, cntr.Status.Get().State())

			_, err := c.RemoveContainer(t.Context(), &runtime.RemoveContainerRequest{ContainerId: containerID})
			require.NoError(t, err)
		})
	}
}

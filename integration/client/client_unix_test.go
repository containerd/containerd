//go:build !windows

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

package client

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types/runc/options"
	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

var (
	testImage             = images.Get(images.BusyBox)
	testMultiLayeredImage = images.Get(images.VolumeCopyUp)
	shortCommand          = withProcessArgs("true")
	// NOTE: The TestContainerPids needs two running processes in one
	// container. But busybox:1.36 sh shell, the `sleep` is a builtin.
	//
	// 	/bin/sh -c "type sleep"
	//      sleep is a shell builtin
	//
	// We should use `/bin/sleep` instead of `sleep`. And busybox sh shell
	// will execve directly instead of clone-execve if there is only one
	// command. There will be only one process in container if we use
	// '/bin/sh -c "/bin/sleep inf"'.
	//
	// So we append `&& exit 0` to force sh shell uses clone-execve.
	longCommand = withProcessArgs("/bin/sh", "-c", "/bin/sleep inf && exit 0")
)

func TestNewTaskWithRuntimeOption(t *testing.T) {
	t.Parallel()

	fakeTasks := &fakeTaskService{
		TasksClient:    tasks.NewTasksClient(nil),
		createRequests: map[string]*tasks.CreateTaskRequest{},
	}

	cli, err := newClient(t, address,
		WithServices(WithTaskClient(fakeTasks)),
	)
	require.NoError(t, err)
	defer cli.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
	)
	defer cancel()

	image, err = cli.GetImage(ctx, testImage)
	require.NoError(t, err)

	for _, tc := range []struct {
		name            string
		runtimeOption   *options.Options
		taskOpts        []NewTaskOpts
		expectedOptions *options.Options
	}{
		{
			name: "should be empty options",
			runtimeOption: &options.Options{
				BinaryName: "no-runc",
			},
			expectedOptions: nil,
		},
		{
			name: "should overwrite IOUid/ShimCgroup",
			runtimeOption: &options.Options{
				BinaryName:    "no-runc",
				ShimCgroup:    "/abc",
				IoUid:         1000,
				SystemdCgroup: true,
			},
			taskOpts: []NewTaskOpts{
				WithUIDOwner(2000),
				WithGIDOwner(3000),
				WithShimCgroup("/def"),
			},
			expectedOptions: &options.Options{
				BinaryName:    "no-runc",
				ShimCgroup:    "/def",
				IoUid:         2000,
				IoGid:         3000,
				SystemdCgroup: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := strings.Replace(t.Name(), "/", "_", -1)

			container, err := cli.NewContainer(
				ctx,
				id,
				WithNewSnapshotView(id, image),
				WithNewSpec(oci.WithImageConfig(image), withExitStatus(7)),
				WithRuntime(plugins.RuntimeRuncV2, tc.runtimeOption),
			)
			require.NoError(t, err)
			defer container.Delete(ctx, WithSnapshotCleanup)

			_, err = container.NewTask(ctx, empty(), tc.taskOpts...)
			require.NoError(t, err)

			fakeTasks.Lock()
			req := fakeTasks.createRequests[id]
			fakeTasks.Unlock()

			if tc.expectedOptions == nil {
				require.Nil(t, req.Options)
				return
			}

			gotOptions := &options.Options{}
			require.NoError(t, typeurl.UnmarshalTo(req.Options, gotOptions))
			require.True(t, cmp.Equal(tc.expectedOptions, gotOptions, protobuf.Compare))
		})
	}
}

type fakeTaskService struct {
	sync.Mutex
	createRequests map[string]*tasks.CreateTaskRequest
	tasks.TasksClient
}

func (ts *fakeTaskService) Create(ctx context.Context, in *tasks.CreateTaskRequest, opts ...grpc.CallOption) (*tasks.CreateTaskResponse, error) {
	ts.Lock()
	defer ts.Unlock()

	ts.createRequests[in.ContainerID] = in
	return &tasks.CreateTaskResponse{
		ContainerID: in.ContainerID,
		Pid:         1,
	}, nil
}

func (ts *fakeTaskService) Get(ctx context.Context, in *tasks.GetRequest, opts ...grpc.CallOption) (*tasks.GetResponse, error) {
	return nil, errgrpc.ToGRPC(errdefs.ErrNotFound)
}

func (ts *fakeTaskService) Delete(ctx context.Context, in *tasks.DeleteTaskRequest, opts ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	return nil, errgrpc.ToGRPC(errdefs.ErrNotFound)
}

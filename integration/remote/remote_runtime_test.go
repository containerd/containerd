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

package remote

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type fakeRuntimeService struct {
	runtimeapi.UnimplementedRuntimeServiceServer
	checkpointPod func(context.Context, *runtimeapi.CheckpointPodRequest) (*runtimeapi.CheckpointPodResponse, error)
	restorePod    func(context.Context, *runtimeapi.RestorePodRequest) (*runtimeapi.RestorePodResponse, error)
}

func (f *fakeRuntimeService) Version(context.Context, *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	return &runtimeapi.VersionResponse{RuntimeApiVersion: "v1"}, nil
}

func (f *fakeRuntimeService) CheckpointPod(ctx context.Context, request *runtimeapi.CheckpointPodRequest) (*runtimeapi.CheckpointPodResponse, error) {
	return f.checkpointPod(ctx, request)
}

func (f *fakeRuntimeService) RestorePod(ctx context.Context, request *runtimeapi.RestorePodRequest) (*runtimeapi.RestorePodResponse, error) {
	return f.restorePod(ctx, request)
}

func (f *fakeRuntimeService) GetContainerEvents(_ *runtimeapi.GetEventsRequest, stream grpc.ServerStreamingServer[runtimeapi.ContainerEventResponse]) error {
	if err := stream.Send(&runtimeapi.ContainerEventResponse{
		ContainerEventType: runtimeapi.ContainerEventType_CONTAINER_CREATED_EVENT,
	}); err != nil {
		return err
	}
	return io.EOF
}

func TestNewRuntimeClientConnGetContainerEventsUnix(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/containerd-cri-events-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketPath))
	})

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := listener.Close()
		if err != nil {
			require.ErrorContains(t, err, "use of closed network connection")
		}
	})

	server := grpc.NewServer()
	t.Cleanup(server.Stop)

	runtimeapi.RegisterRuntimeServiceServer(server, &fakeRuntimeService{})
	go func() {
		_ = server.Serve(listener)
	}()

	conn, err := newRuntimeClientConn("unix://" + socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
	})

	client := runtimeapi.NewRuntimeServiceClient(conn)
	stream, err := client.GetContainerEvents(context.Background(), &runtimeapi.GetEventsRequest{})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, runtimeapi.ContainerEventType_CONTAINER_CREATED_EVENT, resp.GetContainerEventType())
}

func TestRuntimeServicePodCheckpointRestoreDeadline(t *testing.T) {
	checkpointRequest := &runtimeapi.CheckpointPodRequest{PodSandboxId: "source", OutputPath: "/checkpoint"}
	restoreRequest := &runtimeapi.RestorePodRequest{CheckpointPath: "/checkpoint"}
	restoreResponse := &runtimeapi.RestorePodResponse{PodSandboxId: "restored"}
	for _, test := range []struct {
		name     string
		request  proto.Message
		response proto.Message
		call     func(*RuntimeService) (proto.Message, error)
	}{
		{
			name:    "CheckpointPod",
			request: checkpointRequest,
			call: func(client *RuntimeService) (proto.Message, error) {
				return nil, client.CheckpointPod(checkpointRequest)
			},
		},
		{
			name:     "RestorePod",
			request:  restoreRequest,
			response: restoreResponse,
			call: func(client *RuntimeService) (proto.Message, error) {
				return client.RestorePod(restoreRequest)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, waitForDeadline := range []bool{false, true} {
				t.Run(fmt.Sprintf("waitForDeadline=%t", waitForDeadline), func(t *testing.T) {
					const timeout = time.Second
					type call struct {
						request  proto.Message
						deadline time.Time
					}
					calls := make(chan call, 1)
					handle := func(ctx context.Context, request proto.Message) error {
						deadline, ok := ctx.Deadline()
						if !ok {
							return status.Error(codes.InvalidArgument, "Pod operation requires a deadline")
						}
						calls <- call{request: request, deadline: deadline}
						if waitForDeadline {
							<-ctx.Done()
							return status.FromContextError(ctx.Err()).Err()
						}
						return nil
					}
					service := &fakeRuntimeService{
						checkpointPod: func(ctx context.Context, request *runtimeapi.CheckpointPodRequest) (*runtimeapi.CheckpointPodResponse, error) {
							return &runtimeapi.CheckpointPodResponse{}, handle(ctx, request)
						},
						restorePod: func(ctx context.Context, request *runtimeapi.RestorePodRequest) (*runtimeapi.RestorePodResponse, error) {
							return restoreResponse, handle(ctx, request)
						},
					}
					socketPath := fmt.Sprintf("/tmp/containerd-cri-pod-%d.sock", time.Now().UnixNano())
					listener, err := net.Listen("unix", socketPath)
					require.NoError(t, err)
					server := grpc.NewServer()
					t.Cleanup(server.Stop)
					runtimeapi.RegisterRuntimeServiceServer(server, service)
					go func() { _ = server.Serve(listener) }()

					client, err := NewRuntimeService("unix://"+socketPath, timeout)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, client.Close(context.Background())) })

					start := time.Now()
					response, err := test.call(client)
					if waitForDeadline {
						require.Equal(t, codes.DeadlineExceeded, status.Code(err))
					} else {
						require.NoError(t, err)
						require.True(t, proto.Equal(test.response, response))
					}
					select {
					case observed := <-calls:
						require.True(t, proto.Equal(test.request, observed.request))
						require.True(t, observed.deadline.After(start))
						require.WithinDuration(t, start.Add(timeout), observed.deadline, timeout/2)
					default:
						t.Fatal("Pod operation did not reach the server with a deadline")
					}
				})
			}
		})
	}
}

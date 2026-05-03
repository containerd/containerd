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
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type fakeRuntimeService struct {
	runtimeapi.UnimplementedRuntimeServiceServer
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

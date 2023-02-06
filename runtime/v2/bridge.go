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

package v2

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	v2 "github.com/containerd/containerd/api/runtime/task/v2"
	v3 "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/ttrpc"
)

// NewTaskClient returns a new task client interface which handles both GRPC and TTRPC servers depending on the
// client object type passed in.
//
// Supported client types are:
// - *ttrpc.Client
// - grpc.ClientConnInterface
//
// In 1.7 we support TaskService v2 (for backward compatibility with existing shims) and GRPC TaskService v3.
// In 2.0 we'll switch to TaskService v3 only for both TTRPC and GRPC, which will remove overhead of mapping v2 structs to v3 structs.
func NewTaskClient(client interface{}) (v2.TaskService, error) {
	switch c := client.(type) {
	case *ttrpc.Client:
		return v2.NewTaskClient(c), nil
	case grpc.ClientConnInterface:
		return &grpcBridge{v3.NewTaskClient(c)}, nil
	default:
		return nil, fmt.Errorf("unsupported shim client type %T", c)
	}
}

// grpcBridge implements `v2.TaskService` interface for GRPC shim server.
type grpcBridge struct {
	client v3.TaskClient
}

var _ v2.TaskService = (*grpcBridge)(nil)

func (g *grpcBridge) State(ctx context.Context, request *v2.StateRequest) (*v2.StateResponse, error) {
	resp, err := g.client.State(ctx, &v3.StateRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
	})

	return &v2.StateResponse{
		ID:         resp.GetID(),
		Bundle:     resp.GetBundle(),
		Pid:        resp.GetPid(),
		Status:     resp.GetStatus(),
		Stdin:      resp.GetStdin(),
		Stdout:     resp.GetStdout(),
		Stderr:     resp.GetStderr(),
		Terminal:   resp.GetTerminal(),
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt(),
		ExecID:     resp.GetExecID(),
	}, err
}

func (g *grpcBridge) Create(ctx context.Context, request *v2.CreateTaskRequest) (*v2.CreateTaskResponse, error) {
	resp, err := g.client.Create(ctx, &v3.CreateTaskRequest{
		ID:               request.GetID(),
		Bundle:           request.GetBundle(),
		Rootfs:           request.GetRootfs(),
		Terminal:         request.GetTerminal(),
		Stdin:            request.GetStdin(),
		Stdout:           request.GetStdout(),
		Stderr:           request.GetStderr(),
		Checkpoint:       request.GetCheckpoint(),
		ParentCheckpoint: request.GetParentCheckpoint(),
		Options:          request.GetOptions(),
	})

	return &v2.CreateTaskResponse{Pid: resp.GetPid()}, err
}

func (g *grpcBridge) Start(ctx context.Context, request *v2.StartRequest) (*v2.StartResponse, error) {
	resp, err := g.client.Start(ctx, &v3.StartRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
	})

	return &v2.StartResponse{Pid: resp.GetPid()}, err
}

func (g *grpcBridge) Delete(ctx context.Context, request *v2.DeleteRequest) (*v2.DeleteResponse, error) {
	resp, err := g.client.Delete(ctx, &v3.DeleteRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
	})

	return &v2.DeleteResponse{
		Pid:        resp.GetPid(),
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt(),
	}, err
}

func (g *grpcBridge) Pids(ctx context.Context, request *v2.PidsRequest) (*v2.PidsResponse, error) {
	resp, err := g.client.Pids(ctx, &v3.PidsRequest{ID: request.GetID()})
	return &v2.PidsResponse{Processes: resp.GetProcesses()}, err
}

func (g *grpcBridge) Pause(ctx context.Context, request *v2.PauseRequest) (*emptypb.Empty, error) {
	return g.client.Pause(ctx, &v3.PauseRequest{ID: request.GetID()})
}

func (g *grpcBridge) Resume(ctx context.Context, request *v2.ResumeRequest) (*emptypb.Empty, error) {
	return g.client.Resume(ctx, &v3.ResumeRequest{ID: request.GetID()})
}

func (g *grpcBridge) Checkpoint(ctx context.Context, request *v2.CheckpointTaskRequest) (*emptypb.Empty, error) {
	return g.client.Checkpoint(ctx, &v3.CheckpointTaskRequest{
		ID:      request.GetID(),
		Path:    request.GetPath(),
		Options: request.GetOptions(),
	})
}

func (g *grpcBridge) Kill(ctx context.Context, request *v2.KillRequest) (*emptypb.Empty, error) {
	return g.client.Kill(ctx, &v3.KillRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
		Signal: request.GetSignal(),
		All:    request.GetAll(),
	})
}

func (g *grpcBridge) Exec(ctx context.Context, request *v2.ExecProcessRequest) (*emptypb.Empty, error) {
	return g.client.Exec(ctx, &v3.ExecProcessRequest{
		ID:       request.GetID(),
		ExecID:   request.GetExecID(),
		Terminal: request.GetTerminal(),
		Stdin:    request.GetStdin(),
		Stdout:   request.GetStdout(),
		Stderr:   request.GetStderr(),
		Spec:     request.GetSpec(),
	})
}

func (g *grpcBridge) ResizePty(ctx context.Context, request *v2.ResizePtyRequest) (*emptypb.Empty, error) {
	return g.client.ResizePty(ctx, &v3.ResizePtyRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
		Width:  request.GetWidth(),
		Height: request.GetHeight(),
	})
}

func (g *grpcBridge) CloseIO(ctx context.Context, request *v2.CloseIORequest) (*emptypb.Empty, error) {
	return g.client.CloseIO(ctx, &v3.CloseIORequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
		Stdin:  request.GetStdin(),
	})
}

func (g *grpcBridge) Update(ctx context.Context, request *v2.UpdateTaskRequest) (*emptypb.Empty, error) {
	return g.client.Update(ctx, &v3.UpdateTaskRequest{
		ID:          request.GetID(),
		Resources:   request.GetResources(),
		Annotations: request.GetAnnotations(),
	})
}

func (g *grpcBridge) Wait(ctx context.Context, request *v2.WaitRequest) (*v2.WaitResponse, error) {
	resp, err := g.client.Wait(ctx, &v3.WaitRequest{
		ID:     request.GetID(),
		ExecID: request.GetExecID(),
	})

	return &v2.WaitResponse{
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt(),
	}, err
}

func (g *grpcBridge) Stats(ctx context.Context, request *v2.StatsRequest) (*v2.StatsResponse, error) {
	resp, err := g.client.Stats(ctx, &v3.StatsRequest{ID: request.GetID()})
	return &v2.StatsResponse{Stats: resp.GetStats()}, err
}

func (g *grpcBridge) Connect(ctx context.Context, request *v2.ConnectRequest) (*v2.ConnectResponse, error) {
	resp, err := g.client.Connect(ctx, &v3.ConnectRequest{ID: request.GetID()})

	return &v2.ConnectResponse{
		ShimPid: resp.GetShimPid(),
		TaskPid: resp.GetTaskPid(),
		Version: resp.GetVersion(),
	}, err
}

func (g *grpcBridge) Shutdown(ctx context.Context, request *v2.ShutdownRequest) (*emptypb.Empty, error) {
	return g.client.Shutdown(ctx, &v3.ShutdownRequest{
		ID:  request.GetID(),
		Now: request.GetNow(),
	})
}

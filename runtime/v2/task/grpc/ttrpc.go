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

package grpc

import (
	gocontext "context"

	containerd_task_v2 "github.com/containerd/containerd/runtime/v2/task"
	google_protobuf "github.com/gogo/protobuf/types"
	grpc1 "google.golang.org/grpc"

	"github.com/containerd/containerd/runtime/v2/task/ttrpc"
)

type ttrpcWrapper struct {
	client TaskClient
}

func NewTtrpcTaskClient(cc *grpc1.ClientConn) ttrpc.TaskService {
	return &ttrpcWrapper{&taskClient{cc}}
}

func (w *ttrpcWrapper) State(ctx gocontext.Context, req *containerd_task_v2.StateRequest) (*containerd_task_v2.StateResponse, error) {
	return w.client.State(ctx, req)
}

func (w *ttrpcWrapper) Create(ctx gocontext.Context, req *containerd_task_v2.CreateTaskRequest) (*containerd_task_v2.CreateTaskResponse, error) {
	return w.client.Create(ctx, req)
}

func (w *ttrpcWrapper) Start(ctx gocontext.Context, req *containerd_task_v2.StartRequest) (*containerd_task_v2.StartResponse, error) {
	return w.client.Start(ctx, req)
}

func (w *ttrpcWrapper) Delete(ctx gocontext.Context, req *containerd_task_v2.DeleteRequest) (*containerd_task_v2.DeleteResponse, error) {
	return w.client.Delete(ctx, req)
}

func (w *ttrpcWrapper) Pids(ctx gocontext.Context, req *containerd_task_v2.PidsRequest) (*containerd_task_v2.PidsResponse, error) {
	return w.client.Pids(ctx, req)
}

func (w *ttrpcWrapper) Pause(ctx gocontext.Context, req *containerd_task_v2.PauseRequest) (*google_protobuf.Empty, error) {
	return w.client.Pause(ctx, req)
}

func (w *ttrpcWrapper) Resume(ctx gocontext.Context, req *containerd_task_v2.ResumeRequest) (*google_protobuf.Empty, error) {
	return w.client.Resume(ctx, req)
}

func (w *ttrpcWrapper) Checkpoint(ctx gocontext.Context, req *containerd_task_v2.CheckpointTaskRequest) (*google_protobuf.Empty, error) {
	return w.client.Checkpoint(ctx, req)
}

func (w *ttrpcWrapper) Kill(ctx gocontext.Context, req *containerd_task_v2.KillRequest) (*google_protobuf.Empty, error) {
	return w.client.Kill(ctx, req)
}

func (w *ttrpcWrapper) Exec(ctx gocontext.Context, req *containerd_task_v2.ExecProcessRequest) (*google_protobuf.Empty, error) {
	return w.client.Exec(ctx, req)
}
func (w *ttrpcWrapper) ResizePty(ctx gocontext.Context, req *containerd_task_v2.ResizePtyRequest) (*google_protobuf.Empty, error) {
	return w.client.ResizePty(ctx, req)
}
func (w *ttrpcWrapper) CloseIO(ctx gocontext.Context, req *containerd_task_v2.CloseIORequest) (*google_protobuf.Empty, error) {
	return w.client.CloseIO(ctx, req)
}

func (w *ttrpcWrapper) Update(ctx gocontext.Context, req *containerd_task_v2.UpdateTaskRequest) (*google_protobuf.Empty, error) {
	return w.client.Update(ctx, req)
}

func (w *ttrpcWrapper) Wait(ctx gocontext.Context, req *containerd_task_v2.WaitRequest) (*containerd_task_v2.WaitResponse, error) {
	return w.client.Wait(ctx, req)
}

func (w *ttrpcWrapper) Stats(ctx gocontext.Context, req *containerd_task_v2.StatsRequest) (*containerd_task_v2.StatsResponse, error) {
	return w.client.Stats(ctx, req)
}

func (w *ttrpcWrapper) Connect(ctx gocontext.Context, req *containerd_task_v2.ConnectRequest) (*containerd_task_v2.ConnectResponse, error) {
	return w.client.Connect(ctx, req)
}

func (w *ttrpcWrapper) Shutdown(ctx gocontext.Context, req *containerd_task_v2.ShutdownRequest) (*google_protobuf.Empty, error) {
	return w.client.Shutdown(ctx, req)
}

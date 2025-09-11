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

package example

import (
	"context"
	"io"
	"os"

	taskAPI "github.com/containerd/containerd/api/runtime/task/v2"
	apitypes "github.com/containerd/containerd/api/types"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			pp, err := ic.GetByID(plugins.EventPlugin, "publisher")
			if err != nil {
				return nil, err
			}
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			return newTaskService(ic.Context, pp.(shim.Publisher), ss.(shutdown.Service))
		},
	})
}

func NewManager(name string) shim.Manager {
	return manager{name: name}
}

type manager struct {
	name string
}

func (m manager) Name() string {
	return m.name
}

func (m manager) Start(context.Context, string, shim.StartOpts) (shim.BootstrapParams, error) {
	return shim.BootstrapParams{}, errdefs.ErrNotImplemented
}

func (m manager) Stop(context.Context, string) (shim.StopStatus, error) {
	return shim.StopStatus{}, errdefs.ErrNotImplemented
}

func (m manager) Info(context.Context, io.Reader) (*apitypes.RuntimeInfo, error) {
	info := &apitypes.RuntimeInfo{
		Name: "io.containerd.example.v1",
		Version: &apitypes.RuntimeVersion{
			Version: "v1.0.0",
		},
	}
	return info, nil
}

func newTaskService(context.Context, shim.Publisher, shutdown.Service) (taskAPI.TaskService, error) {
	// The shim.Publisher and shutdown.Service are usually useful for your task service,
	// but we don't need them in the exampleTaskService.
	return &exampleTaskService{}, nil
}

var (
	_ = shim.TTRPCService(&exampleTaskService{})
)

type exampleTaskService struct {
}

// RegisterTTRPC allows TTRPC services to be registered with the underlying server
func (s *exampleTaskService) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTaskService(server, s)
	return nil
}

// Create a new container
func (s *exampleTaskService) Create(context.Context, *taskAPI.CreateTaskRequest) (_ *taskAPI.CreateTaskResponse, err error) {
	return nil, errdefs.ErrNotImplemented
}

// Start the primary user process inside the container
func (s *exampleTaskService) Start(context.Context, *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Delete a process or container
func (s *exampleTaskService) Delete(context.Context, *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Exec an additional process inside the container
func (s *exampleTaskService) Exec(context.Context, *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// ResizePty of a process
func (s *exampleTaskService) ResizePty(context.Context, *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// State returns runtime state of a process
func (s *exampleTaskService) State(context.Context, *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Pause the container
func (s *exampleTaskService) Pause(context.Context, *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Resume the container
func (s *exampleTaskService) Resume(context.Context, *taskAPI.ResumeRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Kill a process
func (s *exampleTaskService) Kill(context.Context, *taskAPI.KillRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Pids returns all pids inside the container
func (s *exampleTaskService) Pids(context.Context, *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// CloseIO of a process
func (s *exampleTaskService) CloseIO(context.Context, *taskAPI.CloseIORequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Checkpoint the container
func (s *exampleTaskService) Checkpoint(context.Context, *taskAPI.CheckpointTaskRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Connect returns shim information of the underlying service
func (s *exampleTaskService) Connect(context.Context, *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Shutdown is called after the underlying resources of the shim are cleaned up and the service can be stopped
func (s *exampleTaskService) Shutdown(context.Context, *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	os.Exit(0)
	return &ptypes.Empty{}, nil
}

// Stats returns container level system stats for a container and its processes
func (s *exampleTaskService) Stats(context.Context, *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Update the live container
func (s *exampleTaskService) Update(context.Context, *taskAPI.UpdateTaskRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Wait for a process to exit
func (s *exampleTaskService) Wait(context.Context, *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

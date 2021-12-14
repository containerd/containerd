//go:build linux
// +build linux

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

	"github.com/containerd/containerd/pkg/shutdown"
	"github.com/containerd/containerd/runtime/v2/runc/manager"
	"github.com/containerd/containerd/runtime/v2/runc/task"
	"github.com/containerd/containerd/runtime/v2/shim"
	shimapi "github.com/containerd/containerd/runtime/v2/task"
)

// TODO(2.0): Remove this package

type shimTaskManager struct {
	shimapi.TaskService
	id      string
	manager shim.Manager
}

func (stm *shimTaskManager) Cleanup(ctx context.Context) (*shimapi.DeleteResponse, error) {
	ss, err := stm.manager.Stop(ctx, stm.id)
	if err != nil {
		return nil, err
	}
	return &shimapi.DeleteResponse{
		Pid:        uint32(ss.Pid),
		ExitStatus: uint32(ss.ExitStatus),
		ExitedAt:   ss.ExitedAt,
	}, nil
}

func (stm *shimTaskManager) StartShim(ctx context.Context, opts shim.StartOpts) (string, error) {
	return stm.manager.Start(ctx, opts.ID, opts)
}

// New returns a new shim service that can be used for
// - serving the task service over grpc/ttrpc
// - shim management
// This function is deprecated in favor direct creation
// of shim manager and registering task service via plugins.
func New(ctx context.Context, id string, publisher shim.Publisher, fn func()) (shim.Shim, error) {
	sd, ok := ctx.(shutdown.Service)
	if !ok {
		ctx, sd = shutdown.WithShutdown(ctx)
		sd.RegisterCallback(func(context.Context) error {
			fn()
			return nil
		})
	}
	ts, err := task.NewTaskService(ctx, publisher, sd)
	if err != nil {
		return nil, err
	}
	return &shimTaskManager{
		TaskService: ts,
		id:          id,
		manager:     manager.NewShimManager("runc"),
	}, nil
}

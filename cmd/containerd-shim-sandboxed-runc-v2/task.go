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

	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/task"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
)

// The containers of the pod run through the task service of
// containerd-shim-runc-v2, the very same implementation: this file only wraps
// it. Nothing about how containers are created, started, exec'd or deleted
// changes.
//
// The one thing the wrapper overrides is who ends the shim. containerd sends
// the task Shutdown RPC after every task deletion, and the shared task
// service exits the shim as soon as no container is left. That is right for a
// shim that exists for its tasks and wrong for a shim whose sandbox outlives
// them: a pod with no containers is still a pod. Here Shutdown is
// acknowledged and ignored; the shim exits when its sandbox is shut down
// (see the sandbox package).
//
// TODO: consolidate with containerd-shim-runc-v2 so that both shims register
// the same task service without a wrapper, e.g. by letting the host of the
// shared service decide the shim lifetime. Wrapping the shutdown.Service the
// shared service receives (a no-op Shutdown, everything else delegated) would
// work as well; the RPC wrapper is kept because it states the intent in one
// place.

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			pp, err := ic.GetByID(plugins.EventPlugin, "publisher")
			if err != nil {
				return nil, err
			}
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			svc, err := task.NewTaskService(ic.Context, pp.(shim.Publisher), ss.(shutdown.Service))
			if err != nil {
				return nil, err
			}
			return &taskService{TTRPCTaskService: svc}, nil
		},
	})
}

// taskService is the task service of containerd-shim-runc-v2 with the shim
// lifetime taken out of its hands. Every RPC but Shutdown is the embedded one.
type taskService struct {
	taskAPI.TTRPCTaskService
}

var _ shim.TTRPCService = (*taskService)(nil)

// RegisterTTRPC implements shim.TTRPCService.
func (s *taskService) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTTRPCTaskService(server, s)
	return nil
}

// Shutdown acknowledges the request and keeps the shim running: the sandbox
// decides when the shim exits.
func (s *taskService) Shutdown(context.Context, *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	return &ptypes.Empty{}, nil
}

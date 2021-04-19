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

package sandbox

import (
	"context"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/services"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.SandboxControllerService,
		Requires: []plugin.Type{
			plugin.RuntimePluginV2,
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			v2r, err := ic.Get(plugin.RuntimePluginV2)
			if err != nil {
				return nil, err
			}

			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			var (
				taskManager    = v2r.(*v2.TaskManager)
				sandboxManager = v2.NewSandboxManager(taskManager)
				db             = m.(*metadata.DB)
				store          = metadata.NewSandboxStore(db)
			)

			return &controllerLocal{
				runtime:   sandboxManager,
				store:     store,
				publisher: ic.Events,
			}, nil
		},
	})
}

type controllerLocal struct {
	runtime   runtime.SandboxRuntime
	store     sandbox.Store
	publisher events.Publisher
}

var _ api.ControllerClient = (*controllerLocal)(nil)

func (c *controllerLocal) Start(ctx context.Context, in *api.ControllerStartRequest, opts ...grpc.CallOption) (*api.ControllerStartResponse, error) {
	sb, err := c.store.Get(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	options := runtime.SandboxOpts{
		RuntimeName: sb.Runtime.Name,
		RuntimeOpts: sb.Runtime.Options,
		Spec:        sb.Spec,
	}

	if _, err := c.runtime.Start(ctx, in.SandboxID, options); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerStartResponse{}, nil
}

func (c *controllerLocal) Shutdown(ctx context.Context, in *api.ControllerShutdownRequest, _ ...grpc.CallOption) (*api.ControllerShutdownResponse, error) {
	if err := c.runtime.Shutdown(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerShutdownResponse{}, nil
}

func (c *controllerLocal) Pause(ctx context.Context, in *api.ControllerPauseRequest, opts ...grpc.CallOption) (*api.ControllerPauseResponse, error) {
	if err := c.runtime.Pause(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerPauseResponse{}, nil
}

func (c *controllerLocal) Resume(ctx context.Context, in *api.ControllerResumeRequest, opts ...grpc.CallOption) (*api.ControllerResumeResponse, error) {
	if err := c.runtime.Resume(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerResumeResponse{}, nil
}

func (c *controllerLocal) Ping(ctx context.Context, in *api.ControllerPingRequest, opts ...grpc.CallOption) (*api.ControllerPingResponse, error) {
	if err := c.runtime.Ping(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerPingResponse{}, nil
}

func (c *controllerLocal) Status(ctx context.Context, in *api.ControllerStatusRequest, opts ...grpc.CallOption) (*api.ControllerStatusResponse, error) {
	status, err := c.runtime.Status(ctx, in.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerStatusResponse{Status: status}, nil
}

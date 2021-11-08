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
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
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
			plugin.RuntimeShimPlugin,
			plugin.MetadataPlugin,
			plugin.EventPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimPlugin, err := ic.Get(plugin.RuntimeShimPlugin)
			if err != nil {
				return nil, err
			}

			metadataPlugin, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			exchangePlugin, err := ic.GetByID(plugin.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}

			var (
				shims     = shimPlugin.(*v2.ShimManager)
				publisher = exchangePlugin.(*exchange.Exchange)
				db        = metadataPlugin.(*metadata.DB)
				store     = metadata.NewSandboxStore(db)
			)

			return &controllerLocal{
				shims:     shims,
				store:     store,
				publisher: publisher,
			}, nil
		},
	})
}

type controllerLocal struct {
	shims     *v2.ShimManager
	store     sandbox.Store
	publisher events.Publisher
}

var _ api.ControllerClient = (*controllerLocal)(nil)

func (c *controllerLocal) Start(ctx context.Context, in *api.ControllerStartRequest, opts ...grpc.CallOption) (*api.ControllerStartResponse, error) {
	panic("implement me")
}

func (c *controllerLocal) Shutdown(ctx context.Context, in *api.ControllerShutdownRequest, opts ...grpc.CallOption) (*api.ControllerShutdownResponse, error) {
	panic("implement me")
}

func (c *controllerLocal) Pause(ctx context.Context, in *api.ControllerPauseRequest, opts ...grpc.CallOption) (*api.ControllerPauseResponse, error) {
	panic("implement me")
}

func (c *controllerLocal) Resume(ctx context.Context, in *api.ControllerResumeRequest, opts ...grpc.CallOption) (*api.ControllerResumeResponse, error) {
	panic("implement me")
}

func (c *controllerLocal) Ping(ctx context.Context, in *api.ControllerPingRequest, opts ...grpc.CallOption) (*api.ControllerPingResponse, error) {
	panic("implement me")
}

func (c *controllerLocal) Status(ctx context.Context, in *api.ControllerStatusRequest, opts ...grpc.CallOption) (*api.ControllerStatusResponse, error) {
	panic("implement me")
}

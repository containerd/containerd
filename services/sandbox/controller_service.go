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
	"errors"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "sandbox-controllers",
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			plugins, err := ic.GetByType(plugin.ServicePlugin)
			if err != nil {
				return nil, err
			}

			p, ok := plugins[services.SandboxControllerService]
			if !ok {
				return nil, errors.New("sandbox service not found")
			}

			i, err := p.Instance()
			if err != nil {
				return nil, err
			}

			return &controllerService{
				local: i.(api.ControllerClient),
			}, nil
		},
	})
}

type controllerService struct {
	local api.ControllerClient
	api.UnimplementedControllerServer
}

var _ api.ControllerServer = (*controllerService)(nil)

func (s *controllerService) Register(server *grpc.Server) error {
	api.RegisterControllerServer(server, s)
	return nil
}

func (s *controllerService) Create(ctx context.Context, req *api.ControllerCreateRequest) (*api.ControllerCreateResponse, error) {
	log.G(ctx).WithField("req", req).Debug("create sandbox")
	return s.local.Create(ctx, req)
}

func (s *controllerService) Start(ctx context.Context, req *api.ControllerStartRequest) (*api.ControllerStartResponse, error) {
	log.G(ctx).WithField("req", req).Debug("start sandbox")
	return s.local.Start(ctx, req)
}

func (s *controllerService) Stop(ctx context.Context, req *api.ControllerStopRequest) (*api.ControllerStopResponse, error) {
	log.G(ctx).WithField("req", req).Debug("delete sandbox")
	return s.local.Stop(ctx, req)
}

func (s *controllerService) Wait(ctx context.Context, req *api.ControllerWaitRequest) (*api.ControllerWaitResponse, error) {
	log.G(ctx).WithField("req", req).Debug("wait sandbox")
	return s.local.Wait(ctx, req)
}

func (s *controllerService) Status(ctx context.Context, req *api.ControllerStatusRequest) (*api.ControllerStatusResponse, error) {
	log.G(ctx).WithField("req", req).Debug("sandbox status")
	return s.local.Status(ctx, req)
}

func (s *controllerService) Delete(ctx context.Context, req *api.ControllerDeleteRequest) (*api.ControllerDeleteResponse, error) {
	log.G(ctx).WithField("req", req).Debug("delete sandbox")
	return s.local.Delete(ctx, req)
}

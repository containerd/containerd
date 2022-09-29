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

package pause

import (
	"context"

	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/pkg/shutdown"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/ttrpc"

	log "github.com/sirupsen/logrus"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.TTRPCPlugin,
		ID:   "pause",
		Requires: []plugin.Type{
			plugin.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ss, err := ic.GetByID(plugin.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}

			return &pauseService{
				shutdown: ss.(shutdown.Service),
			}, nil
		},
	})
}

// pauseService is an extension for task v2 runtime to support Pod "pause" containers via sandbox API.
type pauseService struct {
	shutdown shutdown.Service
}

var _ api.SandboxService = (*pauseService)(nil)

func (p *pauseService) RegisterTTRPC(server *ttrpc.Server) error {
	api.RegisterSandboxService(server, p)
	return nil
}

func (p *pauseService) StartSandbox(ctx context.Context, req *api.StartSandboxRequest) (*api.StartSandboxResponse, error) {
	log.Debugf("start sandbox request: %+v", req)
	return &api.StartSandboxResponse{}, nil
}

func (p *pauseService) StopSandbox(ctx context.Context, req *api.StopSandboxRequest) (*api.StopSandboxResponse, error) {
	log.Debugf("stop sandbox request: %+v", req)
	p.shutdown.Shutdown()
	return &api.StopSandboxResponse{}, nil
}

func (p *pauseService) WaitSandbox(ctx context.Context, req *api.WaitSandboxRequest) (*api.WaitSandboxResponse, error) {
	return &api.WaitSandboxResponse{
		ExitStatus: 0,
	}, nil
}

func (p *pauseService) UpdateSandbox(ctx context.Context, req *api.UpdateSandboxRequest) (*api.UpdateSandboxResponse, error) {
	log.Debugf("update sandbox request: %+v", req)
	return &api.UpdateSandboxResponse{}, nil
}

func (p *pauseService) PauseSandbox(ctx context.Context, req *api.PauseSandboxRequest) (*api.PauseSandboxResponse, error) {
	log.Debugf("pause sandbox request: %+v", req)
	return &api.PauseSandboxResponse{}, nil
}

func (p *pauseService) ResumeSandbox(ctx context.Context, req *api.ResumeSandboxRequest) (*api.ResumeSandboxResponse, error) {
	log.Debugf("resume sandbox request: %+v", req)
	return &api.ResumeSandboxResponse{}, nil
}

func (p *pauseService) SandboxStatus(ctx context.Context, req *api.SandboxStatusRequest) (*api.SandboxStatusResponse, error) {
	log.Debugf("sandbox status request: %+v", req)
	return &api.SandboxStatusResponse{}, nil
}

func (p *pauseService) PingSandbox(ctx context.Context, req *api.PingRequest) (*api.PingResponse, error) {
	return &api.PingResponse{}, nil
}

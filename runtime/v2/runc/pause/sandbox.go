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

package pause

import (
	"context"
	"runtime"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/shutdown"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/ttrpc"

	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/plugin"
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

var (
	_ = shim.TTRPCService(&pauseService{})
	_ = api.TTRPCSandboxService(&pauseService{})
)

// pauseService is an extension for task v2 runtime to support Pod "pause" containers via sandbox API.
type pauseService struct {
	shutdown shutdown.Service
}

func (p *pauseService) RegisterTTRPC(server *ttrpc.Server) error {
	api.RegisterTTRPCSandboxService(server, p)
	return nil
}

func (p *pauseService) CreateSandbox(ctx context.Context, req *api.CreateSandboxRequest) (*api.CreateSandboxResponse, error) {
	log.G(ctx).Debugf("create sandbox request: %+v", req)
	return &api.CreateSandboxResponse{}, nil
}

func (p *pauseService) StartSandbox(ctx context.Context, req *api.StartSandboxRequest) (*api.StartSandboxResponse, error) {
	log.G(ctx).Debugf("start sandbox request: %+v", req)
	return &api.StartSandboxResponse{}, nil
}

func (p *pauseService) Platform(ctx context.Context, req *api.PlatformRequest) (*api.PlatformResponse, error) {
	log.G(ctx).Debugf("platform request: %+v", req)

	platform := types.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	return &api.PlatformResponse{Platform: &platform}, nil
}

func (p *pauseService) StopSandbox(ctx context.Context, req *api.StopSandboxRequest) (*api.StopSandboxResponse, error) {
	log.G(ctx).Debugf("stop sandbox request: %+v", req)
	p.shutdown.Shutdown()
	return &api.StopSandboxResponse{}, nil
}

func (p *pauseService) WaitSandbox(ctx context.Context, req *api.WaitSandboxRequest) (*api.WaitSandboxResponse, error) {
	log.G(ctx).Debugf("wait sandbox request: %+v", req)
	return &api.WaitSandboxResponse{
		ExitStatus: 0,
	}, nil
}

func (p *pauseService) SandboxStatus(ctx context.Context, req *api.SandboxStatusRequest) (*api.SandboxStatusResponse, error) {
	log.G(ctx).Debugf("sandbox status request: %+v", req)
	return &api.SandboxStatusResponse{}, nil
}

func (p *pauseService) PingSandbox(ctx context.Context, req *api.PingRequest) (*api.PingResponse, error) {
	return &api.PingResponse{}, nil
}

func (p *pauseService) ShutdownSandbox(ctx context.Context, req *api.ShutdownSandboxRequest) (*api.ShutdownSandboxResponse, error) {
	log.G(ctx).Debugf("shutdown sandbox request: %+v", req)
	return &api.ShutdownSandboxResponse{}, nil
}

func (p *pauseService) SandboxMetrics(ctx context.Context, req *api.SandboxMetricsRequest) (*api.SandboxMetricsResponse, error) {
	log.G(ctx).Debugf("sandbox metrics request: %+v", req)
	return &api.SandboxMetricsResponse{}, nil
}

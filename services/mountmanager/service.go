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

package images

import (
	"context"
	"errors"
	"fmt"

	mmapi "github.com/containerd/containerd/api/services/mountmanager/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mountmanager"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/services"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   services.MountManagerService,
		Requires: []plugin.Type{
			plugin.ServicePlugin,
			plugin.MountPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			mountPlugins, err := ic.GetByType(plugin.MountPlugin)
			if errors.Is(err, errdefs.ErrNotFound) {
				log.G(ic.Context).Debugf("no mount plugins registered")
			} else if err != nil {
				return nil, err
			}
			srv := &service{
				mountManagers: make(map[string]mountmanager.MountManager),
			}
			for name, p := range mountPlugins {
				mp, err := p.Instance()
				if err != nil {
					return nil, fmt.Errorf("get instance of mount plugin: %w", err)
				}
				srv.mountManagers[name] = mp.(mountmanager.MountManager)
				log.G(ic.Context).Debugf("initialized mount plugin: %s", name)
			}
			return srv, nil
		},
	})
}

type service struct {
	mountManagers map[string]mountmanager.MountManager
	mmapi.UnimplementedMountManagerServer
}

var _ mmapi.MountManagerServer = &service{}

func (s *service) Register(server *grpc.Server) error {
	mmapi.RegisterMountManagerServer(server, s)
	return nil
}

func (s *service) RegisterTTRPC(server *ttrpc.Server) error {
	mmapi.RegisterTTRPCMountManagerService(server, s)
	return nil
}

func (s *service) Mount(ctx context.Context, req *mmapi.MountRequest) (*mmapi.MountResponse, error) {
	var mtypeManager mountmanager.MountManager
	mtypeManager, ok := s.mountManagers[req.PluginName]
	if !ok {
		return nil, errdefs.ToGRPC(fmt.Errorf("no mount manager plugin for mount type %s", req.PluginName))
	}

	reqData, err := typeurl.UnmarshalAny(req.Data)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resData, err := mtypeManager.Mount(ctx, reqData)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	// TODO(ambarve): should we roll back the mount operation here? How?
	resDataAny, err := typeurl.MarshalAny(resData)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resp := &mmapi.MountResponse{
		PluginName: req.PluginName,
		Data:       protobuf.FromAny(resDataAny),
	}
	return resp, nil
}

func (s *service) Unmount(ctx context.Context, req *mmapi.UnmountRequest) (*mmapi.UnmountResponse, error) {
	var mtypeManager mountmanager.MountManager
	mtypeManager, ok := s.mountManagers[req.PluginName]
	if !ok {
		return nil, errdefs.ToGRPC(fmt.Errorf("no mount manager plugin for mount type %s", req.PluginName))
	}

	reqData, err := typeurl.UnmarshalAny(req.Data)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resData, err := mtypeManager.Unmount(ctx, reqData)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resDataAny, err := typeurl.MarshalAny(resData)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resp := &mmapi.UnmountResponse{
		PluginName: req.PluginName,
		Data:       protobuf.FromAny(resDataAny),
	}
	return resp, nil
}

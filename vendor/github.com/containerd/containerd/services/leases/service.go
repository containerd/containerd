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

package leases

import (
	"google.golang.org/grpc"

	api "github.com/containerd/containerd/api/services/leases/v1"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "leases",
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			plugins, err := ic.GetByType(plugin.ServicePlugin)
			if err != nil {
				return nil, err
			}
			p, ok := plugins[services.LeasesService]
			if !ok {
				return nil, errors.New("leases service not found")
			}
			i, err := p.Instance()
			if err != nil {
				return nil, err
			}
			return &service{local: i.(api.LeasesClient)}, nil
		},
	})
}

type service struct {
	local api.LeasesClient
}

func (s *service) Register(server *grpc.Server) error {
	api.RegisterLeasesServer(server, s)
	return nil
}

func (s *service) Create(ctx context.Context, r *api.CreateRequest) (*api.CreateResponse, error) {
	return s.local.Create(ctx, r)
}

func (s *service) Delete(ctx context.Context, r *api.DeleteRequest) (*ptypes.Empty, error) {
	return s.local.Delete(ctx, r)
}

func (s *service) List(ctx context.Context, r *api.ListRequest) (*api.ListResponse, error) {
	return s.local.List(ctx, r)
}

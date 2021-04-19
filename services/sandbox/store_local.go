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

	"github.com/containerd/containerd/services"
	"google.golang.org/grpc"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/sandbox"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.SandboxStoreService,
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			db := m.(*metadata.DB)
			return &sandboxLocal{
				store:     metadata.NewSandboxStore(db),
				publisher: ic.Events,
			}, nil
		},
	})
}

type sandboxLocal struct {
	store     sandbox.Store
	publisher events.Publisher
}

var _ = (api.StoreClient)(&sandboxLocal{})

func (s *sandboxLocal) Create(ctx context.Context, in *api.StoreCreateRequest, _ ...grpc.CallOption) (*api.StoreCreateResponse, error) {
	sb, err := s.store.Create(ctx, sandbox.FromProto(&in.Sandbox))
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.StoreCreateResponse{Sandbox: sandbox.ToProto(&sb)}, nil
}

func (s *sandboxLocal) Update(ctx context.Context, in *api.StoreUpdateRequest, _ ...grpc.CallOption) (*api.StoreUpdateResponse, error) {
	sb, err := s.store.Update(ctx, sandbox.FromProto(&in.Sandbox), in.Fields...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.StoreUpdateResponse{Sandbox: sandbox.ToProto(&sb)}, nil
}

func (s *sandboxLocal) Get(ctx context.Context, in *api.StoreGetRequest, _ ...grpc.CallOption) (*api.StoreGetResponse, error) {
	resp, err := s.store.Get(ctx, in.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	desc := sandbox.ToProto(&resp)
	return &api.StoreGetResponse{Sandbox: &desc}, nil
}

func (s *sandboxLocal) List(ctx context.Context, in *api.StoreListRequest, _ ...grpc.CallOption) (*api.StoreListResponse, error) {
	resp, err := s.store.List(ctx, in.Filters...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	list := make([]types.Sandbox, len(resp))
	for i, item := range resp {
		list[i] = sandbox.ToProto(&item)
	}

	return &api.StoreListResponse{List: list}, nil
}

func (s *sandboxLocal) Delete(ctx context.Context, in *api.StoreDeleteRequest, _ ...grpc.CallOption) (*api.StoreDeleteResponse, error) {
	if err := s.store.Delete(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.StoreDeleteResponse{}, nil
}

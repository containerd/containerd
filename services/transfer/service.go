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

package transfer

import (
	"context"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "transfer",
		Requires: []plugin.Type{
			plugin.TransferPlugin,
		},
		InitFn: newService,
	})
}

type service struct {
	transferers []transfer.Transferer
	transferapi.UnimplementedTransferServer
}

func newService(ic *plugin.InitContext) (interface{}, error) {
	plugins, err := ic.GetByType(plugin.TransferPlugin)
	if err != nil {
		return nil, err
	}
	// TODO: how to determine order?
	t := make([]transfer.Transferer, 0, len(plugins))
	for _, p := range plugins {
		i, err := p.Instance()
		if err != nil {
			return nil, err
		}
		t = append(t, i.(transfer.Transferer))
	}
	return &service{
		transferers: t,
	}, nil
}

func (s *service) Register(gs *grpc.Server) error {
	transferapi.RegisterTransferServer(gs, s)
	return nil
}

func (s *service) Transfer(ctx context.Context, req *transferapi.TransferRequest) (*emptypb.Empty, error) {
	// TODO: Optionally proxy

	// TODO: Convert options
	for _, t := range s.transferers {
		if err := t.Transfer(ctx, req.Source, req.Destination); err == nil {
			return nil, nil
		} else if !errdefs.IsNotImplemented(err) {
			return nil, errdefs.ToGRPC(err)
		}
	}
	return nil, status.Errorf(codes.Unimplemented, "method Transfer not implemented for %s to %s", req.Source.GetTypeUrl(), req.Destination.GetTypeUrl())
}

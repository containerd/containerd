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

package proxy

import (
	"context"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/typeurl"
	"google.golang.org/protobuf/types/known/anypb"
)

type proxyTransferer struct {
	client transferapi.TransferClient
}

// NewTransferer returns a new transferr which communicates over a GRPC
// connection using the containerd transfer API
func NewTransferer(client transferapi.TransferClient) transfer.Transferer {
	return &proxyTransferer{
		client: client,
	}
}

func (p *proxyTransferer) Transfer(ctx context.Context, src interface{}, dst interface{}, opts ...transfer.Opt) error {
	asrc, err := typeurl.MarshalAny(src)
	if err != nil {
		return err
	}
	adst, err := typeurl.MarshalAny(dst)
	if err != nil {
		return err
	}
	// Resolve opts to
	req := &transferapi.TransferRequest{
		Source: &anypb.Any{
			TypeUrl: asrc.GetTypeUrl(),
			Value:   asrc.GetValue(),
		},
		Destination: &anypb.Any{
			TypeUrl: adst.GetTypeUrl(),
			Value:   adst.GetValue(),
		},
		// TODO: Options
	}
	_, err = p.client.Transfer(ctx, req)
	return err
}

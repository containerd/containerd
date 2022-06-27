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

package containerd

import (
	"context"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/typeurl"
	"google.golang.org/protobuf/types/known/anypb"
)

func (c *Client) Transfer(ctx context.Context, src interface{}, dst interface{}, opts ...transfer.Opt) error {
	// Conver Options
	// Convert Source
	// Convert Destinations
	// Get Stream Manager

	asrc, err := c.toAny(ctx, src)
	if err != nil {
		return err
	}

	adst, err := c.toAny(ctx, dst)
	if err != nil {
		return err
	}

	_, err = transferapi.NewTransferClient(c.conn).Transfer(ctx, &transferapi.TransferRequest{
		Source: &anypb.Any{
			TypeUrl: asrc.GetTypeUrl(),
			Value:   asrc.GetValue(),
		},
		Destination: &anypb.Any{
			TypeUrl: adst.GetTypeUrl(),
			Value:   adst.GetValue(),
		},
	})
	return err
}

func (c *Client) toAny(ctx context.Context, i interface{}) (a typeurl.Any, err error) {
	switch v := i.(type) {
	case toAny:
		//Get stream manager
		a, err = v.ToAny(ctx, nil)
	case typeurl.Any:
		a = v
	default:
		a, err = typeurl.MarshalAny(i)
	}

	return
}

type toAny interface {
	ToAny(context.Context, streaming.StreamManager) (typeurl.Any, error)
}

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

package service

import (
	"context"
	"io"

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/errdefs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type PushResponseEnvelope struct {
	*api.PushResponse
	Err error
}

type PushService interface {
	Push(ctx context.Context, req *api.PushRequest) (<-chan *PushResponseEnvelope, error)
}

func NewPushClient(conn *grpc.ClientConn) PushService {
	return &pushClient{api.NewPushServiceClient(conn)}
}

type pushClient struct {
	c api.PushServiceClient
}

func (c *pushClient) Push(ctx context.Context, req *api.PushRequest) (<-chan *PushResponseEnvelope, error) {
	ctx, cancel := context.WithCancel(ctx)

	if v := ctx.Value(remoteCtxKey{}); v != nil {
		ctx = metadata.AppendToOutgoingContext(ctx, mdRemoteKey, v.(string))
	}

	recv, err := c.c.Push(ctx, req)
	if err != nil {
		cancel()
		return nil, errdefs.FromGRPC(err)
	}

	ch := make(chan *PushResponseEnvelope, 1)
	go func() {
		defer close(ch)
		defer cancel()

		for {
			resp, err := recv.Recv()
			e := err
			if e == io.EOF {
				e = nil
			}
			env := &PushResponseEnvelope{Err: errdefs.FromGRPC(e), PushResponse: resp}

			select {
			case <-ctx.Done():
				return
			case ch <- env:
			}

			if err != nil {
				return
			}
		}
	}()

	return ch, nil
}

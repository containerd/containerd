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

type PullResponseEnvelope struct {
	*api.PullResponse
	Err error
}

type PullService interface {
	Pull(ctx context.Context, req *api.PullRequest) (<-chan *PullResponseEnvelope, error)
}

func NewPullClient(conn *grpc.ClientConn) PullService {
	return &pullClient{api.NewPullServiceClient(conn)}
}

type pullClient struct {
	c api.PullServiceClient
}

func (c *pullClient) Pull(ctx context.Context, req *api.PullRequest) (<-chan *PullResponseEnvelope, error) {
	ctx, cancel := context.WithCancel(ctx)

	if v := ctx.Value(remoteCtxKey{}); v != nil {
		ctx = metadata.AppendToOutgoingContext(ctx, mdRemoteKey, v.(string))
	}

	recv, err := c.c.Pull(ctx, req)
	if err != nil {
		cancel()
		return nil, errdefs.FromGRPC(err)
	}

	ch := make(chan *PullResponseEnvelope, 1)
	go func() {
		defer close(ch)
		defer cancel()

		for {
			resp, err := recv.Recv()
			e := err
			if e == io.EOF {
				return
			}
			env := &PullResponseEnvelope{Err: errdefs.FromGRPC(e), PullResponse: resp}

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

package service

import (
	"context"
	"io"

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/errdefs"
	"google.golang.org/grpc"
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

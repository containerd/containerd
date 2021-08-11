package service

import (
	"context"
	"io"

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/errdefs"
	"google.golang.org/grpc"
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

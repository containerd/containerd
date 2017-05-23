package containerd

import (
	"context"
	"io/ioutil"
	"log"
	"time"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

// New returns a new containerd client that is connected to the containerd
// instance provided by address
func New(address string) (*Client, error) {
	// reset the grpc logger so that it does not output in the STDIO of the calling process
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))

	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithTimeout(100 * time.Second),
		grpc.WithDialer(dialer),
	}
	conn, err := grpc.Dial(dialAddress(address), opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return &Client{
		conn: conn,
	}, nil
}

// Client is the client to interact with containerd and its various services
// using a uniform interface
type Client struct {
	conn *grpc.ClientConn
}

// Containers returns all containers created in containerd
func (c *Client) Containers(ctx context.Context) ([]*Container, error) {
	r, err := c.containers().List(ctx, &containers.ListContainersRequest{})
	if err != nil {
		return nil, err
	}
	var out []*Container
	for _, container := range r.Containers {
		out = append(out, containerFromProto(c, container))
	}
	return out, nil
}

// Close closes the clients connection to containerd
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) containers() containers.ContainersClient {
	return containers.NewContainersClient(c.conn)
}

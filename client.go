package containerd

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"time"

	"github.com/containerd/containerd/api/services/containers"
	contentapi "github.com/containerd/containerd/api/services/content"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	contentservice "github.com/containerd/containerd/services/content"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	"github.com/containerd/containerd/snapshot"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
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

type NewContainerOpts func(ctx context.Context, client *Client, c *containers.Container) error

// NewContainerWithLables adds the provided labels to the container
func NewContainerWithLables(labels map[string]string) NewContainerOpts {
	return func(_ context.Context, _ *Client, c *containers.Container) error {
		c.Labels = labels
		return nil
	}
}

// NewContainerWithExistingRootFS uses an existing root filesystem for the container
func NewContainerWithExistingRootFS(id string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		// check that the snapshot exists, if not, fail on creation
		if _, err := client.snapshotter().Mounts(ctx, id); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// NewContainerWithNewRootFS allocates a new snapshot to be used by the container as the
// root filesystem in read-write mode
func NewContainerWithNewRootFS(id string, image v1.Descriptor) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := images.RootFS(ctx, client.content(), image)
		if err != nil {
			return err
		}
		if _, err := client.snapshotter().Prepare(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// NewContainerWithNewReadonlyRootFS allocates a new snapshot to be used by the container as the
// root filesystem in read-only mode
func NewContainerWithNewReadonlyRootFS(id string, image v1.Descriptor) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := images.RootFS(ctx, client.content(), image)
		if err != nil {
			return err
		}
		if _, err := client.snapshotter().View(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// NewContainer will create a new container in container with the provided id
// the id must be unique within the namespace
func (c *Client) NewContainer(ctx context.Context, id string, spec *specs.Spec, opts ...NewContainerOpts) (*Container, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	container := containers.Container{
		ID: id,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		},
	}
	for _, o := range opts {
		if err := o(ctx, c, &container); err != nil {
			return nil, err
		}
	}
	r, err := c.containers().Create(ctx, &containers.CreateContainerRequest{
		Container: container,
	})
	if err != nil {
		return nil, err
	}
	return containerFromProto(c, r.Container), nil
}

// Close closes the clients connection to containerd
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) containers() containers.ContainersClient {
	return containers.NewContainersClient(c.conn)
}

func (c *Client) content() content.Store {
	return contentservice.NewStoreFromClient(contentapi.NewContentClient(c.conn))
}

func (c *Client) snapshotter() snapshot.Snapshotter {
	return snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(c.conn))
}

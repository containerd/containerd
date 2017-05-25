package containerd

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/containerd/containerd/api/services/containers"
	contentapi "github.com/containerd/containerd/api/services/content"
	diffapi "github.com/containerd/containerd/api/services/diff"
	"github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/rootfs"
	contentservice "github.com/containerd/containerd/services/content"
	"github.com/containerd/containerd/services/diff"
	diffservice "github.com/containerd/containerd/services/diff"
	imagesservice "github.com/containerd/containerd/services/images"
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

func init() {
	// reset the grpc logger so that it does not output in the STDIO of the calling process
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
}

type NewClientOpts func(c *Client) error

func WithNamespace(namespace string) NewClientOpts {
	return func(c *Client) error {
		c.namespace = namespace
		return nil
	}
}

// New returns a new containerd client that is connected to the containerd
// instance provided by address
func New(address string, opts ...NewClientOpts) (*Client, error) {
	gopts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithTimeout(100 * time.Second),
		grpc.WithDialer(dialer),
	}
	conn, err := grpc.Dial(dialAddress(address), gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	c := &Client{
		conn:    conn,
		runtime: runtime.GOOS,
	}
	for _, o := range opts {
		if err := o(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Client is the client to interact with containerd and its various services
// using a uniform interface
type Client struct {
	conn *grpc.ClientConn

	runtime   string
	namespace string
}

// Containers returns all containers created in containerd
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	r, err := c.ContainerService().List(ctx, &containers.ListContainersRequest{})
	if err != nil {
		return nil, err
	}
	var out []Container
	for _, container := range r.Containers {
		out = append(out, containerFromProto(c, container))
	}
	return out, nil
}

type NewContainerOpts func(ctx context.Context, client *Client, c *containers.Container) error

// WithContainerLabels adds the provided labels to the container
func WithContainerLabels(labels map[string]string) NewContainerOpts {
	return func(_ context.Context, _ *Client, c *containers.Container) error {
		c.Labels = labels
		return nil
	}
}

// WithExistingRootFS uses an existing root filesystem for the container
func WithExistingRootFS(id string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		// check that the snapshot exists, if not, fail on creation
		if _, err := client.SnapshotService().Mounts(ctx, id); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// WithNewRootFS allocates a new snapshot to be used by the container as the
// root filesystem in read-write mode
func WithNewRootFS(id string, i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore())
		if err != nil {
			return err
		}
		if _, err := client.SnapshotService().Prepare(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// WithNewReadonlyRootFS allocates a new snapshot to be used by the container as the
// root filesystem in read-only mode
func WithNewReadonlyRootFS(id string, i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore())
		if err != nil {
			return err
		}
		if _, err := client.SnapshotService().View(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

func WithRuntime(name string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		c.Runtime = name
		return nil
	}
}

func WithImage(i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		c.Image = i.Name()
		return nil
	}
}

// NewContainer will create a new container in container with the provided id
// the id must be unique within the namespace
func (c *Client) NewContainer(ctx context.Context, id string, spec *specs.Spec, opts ...NewContainerOpts) (Container, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	container := containers.Container{
		ID:      id,
		Runtime: c.runtime,
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
	r, err := c.ContainerService().Create(ctx, &containers.CreateContainerRequest{
		Container: container,
	})
	if err != nil {
		return nil, err
	}
	return containerFromProto(c, r.Container), nil
}

type PullOpts func(*Client, *PullContext) error

type PullContext struct {
	Resolver remotes.Resolver
	Unpacker Unpacker
}

func defaultPullContext() *PullContext {
	return &PullContext{
		Resolver: docker.NewResolver(docker.ResolverOptions{
			Client: http.DefaultClient,
		}),
	}
}

func WithPullUnpack(client *Client, c *PullContext) error {
	c.Unpacker = &snapshotUnpacker{
		store:       client.ContentStore(),
		diff:        client.DiffService(),
		snapshotter: client.SnapshotService(),
	}
	return nil
}

type Unpacker interface {
	Unpack(context.Context, images.Image) error
}

type snapshotUnpacker struct {
	snapshotter snapshot.Snapshotter
	store       content.Store
	diff        diff.DiffService
}

func (s *snapshotUnpacker) Unpack(ctx context.Context, image images.Image) error {
	layers, err := s.getLayers(ctx, image)
	if err != nil {
		return err
	}
	if _, err := rootfs.ApplyLayers(ctx, layers, s.snapshotter, s.diff); err != nil {
		return err
	}
	return nil
}

func (s *snapshotUnpacker) getLayers(ctx context.Context, image images.Image) ([]rootfs.Layer, error) {
	p, err := content.ReadBlob(ctx, s.store, image.Target.Digest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read manifest blob")
	}
	var manifest v1.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal manifest")
	}
	diffIDs, err := image.RootFS(ctx, s.store)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve rootfs")
	}
	if len(diffIDs) != len(manifest.Layers) {
		return nil, errors.Errorf("mismatched image rootfs and manifest layers")
	}
	layers := make([]rootfs.Layer, len(diffIDs))
	for i := range diffIDs {
		layers[i].Diff = v1.Descriptor{
			// TODO: derive media type from compressed type
			MediaType: v1.MediaTypeImageLayer,
			Digest:    diffIDs[i],
		}
		layers[i].Blob = manifest.Layers[i]
	}
	return layers, nil
}

func (c *Client) Pull(ctx context.Context, ref string, opts ...PullOpts) (Image, error) {
	pullCtx := defaultPullContext()
	for _, o := range opts {
		if err := o(c, pullCtx); err != nil {
			return nil, err
		}
	}
	store := c.ContentStore()

	name, desc, err := pullCtx.Resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	fetcher, err := pullCtx.Resolver.Fetcher(ctx, name)
	if err != nil {
		return nil, err
	}

	handlers := []images.Handler{
		remotes.FetchHandler(store, fetcher),
		images.ChildrenHandler(store),
	}
	if err := images.Dispatch(ctx, images.Handlers(handlers...), desc); err != nil {
		return nil, err
	}
	is := c.ImageService()
	if err := is.Put(ctx, name, desc); err != nil {
		return nil, err
	}
	i, err := is.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if pullCtx.Unpacker != nil {
		if err := pullCtx.Unpacker.Unpack(ctx, i); err != nil {
			return nil, err
		}
	}
	return &image{
		client: c,
		i:      i,
	}, nil
}

// Close closes the clients connection to containerd
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) ContainerService() containers.ContainersClient {
	return containers.NewContainersClient(c.conn)
}

func (c *Client) ContentStore() content.Store {
	return contentservice.NewStoreFromClient(contentapi.NewContentClient(c.conn))
}

func (c *Client) SnapshotService() snapshot.Snapshotter {
	return snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(c.conn))
}

func (c *Client) TaskService() execution.TasksClient {
	return execution.NewTasksClient(c.conn)
}

func (c *Client) ImageService() images.Store {
	return imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(c.conn))
}

func (c *Client) DiffService() diff.DiffService {
	return diffservice.NewDiffServiceFromClient(diffapi.NewDiffClient(c.conn))
}

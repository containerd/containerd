package containerd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/mount"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

var (
	ErrNoImage       = errors.New("container does not have an image")
	ErrNoRunningTask = errors.New("no running task")
)

type Container interface {
	ID() string
	Proto() containers.Container
	Delete(context.Context) error
	NewTask(context.Context, IOCreation, ...NewTaskOpts) (Task, error)
	Spec() (*specs.Spec, error)
	Task(context.Context, IOAttach) (Task, error)
	Image(context.Context) (Image, error)
}

func containerFromProto(client *Client, c containers.Container) *container {
	return &container{
		client: client,
		c:      c,
	}
}

var _ = (Container)(&container{})

type container struct {
	mu sync.Mutex

	client *Client
	c      containers.Container
	task   *task
}

// ID returns the container's unique id
func (c *container) ID() string {
	return c.c.ID
}

func (c *container) Proto() containers.Container {
	return c.c
}

// Spec returns the current OCI specification for the container
func (c *container) Spec() (*specs.Spec, error) {
	var s specs.Spec
	if err := json.Unmarshal(c.c.Spec.Value, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Delete deletes an existing container
// an error is returned if the container has running tasks
func (c *container) Delete(ctx context.Context) (err error) {
	// TODO: should the client be the one removing resources attached
	// to the container at the moment before we have GC?
	if c.c.RootFS != "" {
		err = c.client.SnapshotService().Remove(ctx, c.c.RootFS)
	}
	if _, cerr := c.client.ContainerService().Delete(ctx, &containers.DeleteContainerRequest{
		ID: c.c.ID,
	}); err == nil {
		err = cerr
	}
	return err
}

func (c *container) Task(ctx context.Context, attach IOAttach) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.task == nil {
		t, err := c.loadTask(ctx, attach)
		if err != nil {
			return nil, err
		}
		c.task = t.(*task)
	}
	return c.task, nil
}

// Image returns the image that the container is based on
func (c *container) Image(ctx context.Context) (Image, error) {
	if c.c.Image == "" {
		return nil, ErrNoImage
	}
	i, err := c.client.ImageService().Get(ctx, c.c.Image)
	if err != nil {
		return nil, err
	}
	return &image{
		client: c.client,
		i:      i,
	}, nil
}

type NewTaskOpts func(context.Context, *Client, *execution.CreateRequest) error

func (c *container) NewTask(ctx context.Context, ioCreate IOCreation, opts ...NewTaskOpts) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, err := ioCreate()
	if err != nil {
		return nil, err
	}
	request := &execution.CreateRequest{
		ContainerID: c.c.ID,
		Terminal:    i.Terminal,
		Stdin:       i.Stdin,
		Stdout:      i.Stdout,
		Stderr:      i.Stderr,
	}
	if c.c.RootFS != "" {
		// get the rootfs from the snapshotter and add it to the request
		mounts, err := c.client.SnapshotService().Mounts(ctx, c.c.RootFS)
		if err != nil {
			return nil, err
		}
		for _, m := range mounts {
			request.Rootfs = append(request.Rootfs, &mount.Mount{
				Type:    m.Type,
				Source:  m.Source,
				Options: m.Options,
			})
		}
	}
	for _, o := range opts {
		if err := o(ctx, c.client, request); err != nil {
			return nil, err
		}
	}
	t := &task{
		client:      c.client,
		io:          i,
		containerID: c.ID(),
		pidSync:     make(chan struct{}),
	}

	if request.Checkpoint != nil {
		// we need to defer the create call to start
		t.deferred = request
	} else {
		response, err := c.client.TaskService().Create(ctx, request)
		if err != nil {
			return nil, err
		}
		t.pid = response.Pid
		close(t.pidSync)
	}
	c.task = t
	return t, nil
}

func (c *container) loadTask(ctx context.Context, ioAttach IOAttach) (Task, error) {
	response, err := c.client.TaskService().Info(ctx, &execution.InfoRequest{
		ContainerID: c.c.ID,
	})
	if err != nil {
		if grpc.Code(errors.Cause(err)) == codes.NotFound {
			return nil, ErrNoRunningTask
		}
		return nil, err
	}
	var i *IO
	if ioAttach != nil {
		// get the existing fifo paths from the task information stored by the daemon
		paths := &FifoSet{
			Dir: getFifoDir([]string{
				response.Task.Stdin,
				response.Task.Stdout,
				response.Task.Stderr,
			}),
			In:       response.Task.Stdin,
			Out:      response.Task.Stdout,
			Err:      response.Task.Stderr,
			Terminal: response.Task.Terminal,
		}
		if i, err = ioAttach(paths); err != nil {
			return nil, err
		}
	}
	// create and close a channel on load as we already have the pid
	// and don't want to block calls to Wait(), etc...
	ps := make(chan struct{})
	close(ps)
	t := &task{
		client:      c.client,
		io:          i,
		containerID: response.Task.ContainerID,
		pid:         response.Task.Pid,
		pidSync:     ps,
	}
	c.task = t
	return t, nil
}

// getFifoDir looks for any non-empty path for a stdio fifo
// and returns the dir for where it is located
func getFifoDir(paths []string) string {
	for _, p := range paths {
		if p != "" {
			return filepath.Dir(p)
		}
	}
	return ""
}

package containerd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	ptypes "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

var (
	ErrNoImage           = errors.New("container does not have an image")
	ErrNoRunningTask     = errors.New("no running task")
	ErrDeleteRunningTask = errors.New("cannot delete container with running task")
	ErrProcessExited     = errors.New("process already exited")
)

type DeleteOpts func(context.Context, *Client, containers.Container) error

type Container interface {
	ID() string
	Proto() containers.Container
	Delete(context.Context, ...DeleteOpts) error
	NewTask(context.Context, IOCreation, ...NewTaskOpts) (Task, error)
	Spec() (*specs.Spec, error)
	Task(context.Context, IOAttach) (Task, error)
	Image(context.Context) (Image, error)
	Labels(context.Context) (map[string]string, error)
	SetLabels(context.Context, map[string]string) (map[string]string, error)
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
}

// ID returns the container's unique id
func (c *container) ID() string {
	return c.c.ID
}

func (c *container) Proto() containers.Container {
	return c.c
}

func (c *container) Labels(ctx context.Context) (map[string]string, error) {
	resp, err := c.client.ContainerService().Get(ctx, &containers.GetContainerRequest{
		ID: c.ID(),
	})
	if err != nil {
		return nil, err
	}

	c.c = resp.Container

	m := make(map[string]string, len(resp.Container.Labels))
	for k, v := range c.c.Labels {
		m[k] = v
	}

	return m, nil
}

func (c *container) SetLabels(ctx context.Context, labels map[string]string) (map[string]string, error) {
	var req containers.UpdateContainerRequest

	req.Container.ID = c.ID()
	req.Container.Labels = labels

	req.UpdateMask = &ptypes.FieldMask{
		Paths: make([]string, 0, len(labels)),
	}
	// mask off paths so we only muck with the labels encountered in labels.
	// Labels not in the passed in argument will be left alone.
	for k := range labels {
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, strings.Join([]string{"labels", k}, "."))
	}

	resp, err := c.client.ContainerService().Update(ctx, &req)
	if err != nil {
		return nil, err
	}

	c.c = resp.Container // update our local container

	m := make(map[string]string, len(resp.Container.Labels))
	for k, v := range c.c.Labels {
		m[k] = v
	}
	return m, nil
}

// Spec returns the current OCI specification for the container
func (c *container) Spec() (*specs.Spec, error) {
	var s specs.Spec
	if err := json.Unmarshal(c.c.Spec.Value, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WithRootFSDeletion deletes the rootfs allocated for the container
func WithRootFSDeletion(ctx context.Context, client *Client, c containers.Container) error {
	if c.RootFS != "" {
		return client.SnapshotService().Remove(ctx, c.RootFS)
	}
	return nil
}

// Delete deletes an existing container
// an error is returned if the container has running tasks
func (c *container) Delete(ctx context.Context, opts ...DeleteOpts) (err error) {
	if _, err := c.Task(ctx, nil); err == nil {
		return ErrDeleteRunningTask
	}
	for _, o := range opts {
		if err := o(ctx, c.client, c.c); err != nil {
			return err
		}
	}
	if _, cerr := c.client.ContainerService().Delete(ctx, &containers.DeleteContainerRequest{
		ID: c.c.ID,
	}); err == nil {
		err = cerr
	}
	return err
}

func (c *container) Task(ctx context.Context, attach IOAttach) (Task, error) {
	return c.loadTask(ctx, attach)
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

type NewTaskOpts func(context.Context, *Client, *tasks.CreateTaskRequest) error

func (c *container) NewTask(ctx context.Context, ioCreate IOCreation, opts ...NewTaskOpts) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, err := ioCreate()
	if err != nil {
		return nil, err
	}
	request := &tasks.CreateTaskRequest{
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
			request.Rootfs = append(request.Rootfs, &types.Mount{
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
		client: c.client,
		io:     i,
		id:     c.ID(),
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
	}
	return t, nil
}

func (c *container) loadTask(ctx context.Context, ioAttach IOAttach) (Task, error) {
	response, err := c.client.TaskService().Get(ctx, &tasks.GetTaskRequest{
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
		paths := &FIFOSet{
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
	t := &task{
		client: c.client,
		io:     i,
		id:     response.Task.ID,
		pid:    response.Task.Pid,
	}
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

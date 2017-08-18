package containerd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/typeurl"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// DeleteOpts allows the caller to set options for the deletion of a container
type DeleteOpts func(context.Context, *Client, containers.Container) error

// Container is a metadata object for container resources and task creation
type Container interface {
	// ID identifies the container
	ID() string
	// Info returns the underlying container record type
	Info() containers.Container
	// Delete removes the container
	Delete(context.Context, ...DeleteOpts) error
	// NewTask creates a new task based on the container metadata
	NewTask(context.Context, IOCreation, ...NewTaskOpts) (Task, error)
	// Spec returns the OCI runtime specification
	Spec() (*specs.Spec, error)
	// Task returns the current task for the container
	//
	// If IOAttach options are passed the client will reattach to the IO for the running
	// task. If no task exists for the container a NotFound error is returned
	Task(context.Context, IOAttach) (Task, error)
	// Image returns the image that the container is based on
	Image(context.Context) (Image, error)
	// Labels returns the labels set on the container
	Labels(context.Context) (map[string]string, error)
	// SetLabels sets the provided labels for the container and returns the final label set
	SetLabels(context.Context, map[string]string) (map[string]string, error)
}

func containerFromRecord(client *Client, c containers.Container) *container {
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

func (c *container) Info() containers.Container {
	return c.c
}

func (c *container) Labels(ctx context.Context) (map[string]string, error) {
	r, err := c.client.ContainerService().Get(ctx, c.ID())
	if err != nil {
		return nil, err
	}

	c.c = r

	m := make(map[string]string, len(r.Labels))
	for k, v := range c.c.Labels {
		m[k] = v
	}

	return m, nil
}

func (c *container) SetLabels(ctx context.Context, labels map[string]string) (map[string]string, error) {
	container := containers.Container{
		ID:     c.ID(),
		Labels: labels,
	}

	var paths []string
	// mask off paths so we only muck with the labels encountered in labels.
	// Labels not in the passed in argument will be left alone.
	for k := range labels {
		paths = append(paths, strings.Join([]string{"labels", k}, "."))
	}

	r, err := c.client.ContainerService().Update(ctx, container, paths...)
	if err != nil {
		return nil, err
	}

	c.c = r // update our local container

	m := make(map[string]string, len(r.Labels))
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

// Delete deletes an existing container
// an error is returned if the container has running tasks
func (c *container) Delete(ctx context.Context, opts ...DeleteOpts) (err error) {
	if _, err := c.Task(ctx, nil); err == nil {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "cannot delete running task %v", c.ID())
	}
	for _, o := range opts {
		if err := o(ctx, c.client, c.c); err != nil {
			return err
		}
	}

	if cerr := c.client.ContainerService().Delete(ctx, c.ID()); err == nil {
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
		return nil, errors.Wrapf(errdefs.ErrNotFound, "container not created from an image")
	}
	i, err := c.client.ImageService().Get(ctx, c.c.Image)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get image for container")
	}
	return &image{
		client: c.client,
		i:      i,
	}, nil
}

func (c *container) NewTask(ctx context.Context, ioCreate IOCreation, opts ...NewTaskOpts) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, err := ioCreate(c.c.ID)
	if err != nil {
		return nil, err
	}
	cfg := i.Config()
	request := &tasks.CreateTaskRequest{
		ContainerID: c.c.ID,
		Terminal:    cfg.Terminal,
		Stdin:       cfg.Stdin,
		Stdout:      cfg.Stdout,
		Stderr:      cfg.Stderr,
	}
	if c.c.RootFS != "" {
		// get the rootfs from the snapshotter and add it to the request
		mounts, err := c.client.SnapshotService(c.c.Snapshotter).Mounts(ctx, c.c.RootFS)
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
	var info TaskInfo
	for _, o := range opts {
		if err := o(ctx, c.client, &info); err != nil {
			return nil, err
		}
	}
	if info.RootFS != nil {
		for _, m := range info.RootFS {
			request.Rootfs = append(request.Rootfs, &types.Mount{
				Type:    m.Type,
				Source:  m.Source,
				Options: m.Options,
			})
		}
	}
	if info.Options != nil {
		any, err := typeurl.MarshalAny(info.Options)
		if err != nil {
			return nil, err
		}
		request.Options = any
	}
	t := &task{
		client: c.client,
		io:     i,
		id:     c.ID(),
	}
	if info.Checkpoint != nil {
		request.Checkpoint = info.Checkpoint
		// we need to defer the create call to start
		t.deferred = request
	} else {
		response, err := c.client.TaskService().Create(ctx, request)
		if err != nil {
			return nil, errdefs.FromGRPC(err)
		}
		t.pid = response.Pid
	}
	return t, nil
}

func (c *container) loadTask(ctx context.Context, ioAttach IOAttach) (Task, error) {
	response, err := c.client.TaskService().Get(ctx, &tasks.GetRequest{
		ContainerID: c.c.ID,
	})
	if err != nil {
		err = errdefs.FromGRPC(err)
		if errdefs.IsNotFound(err) {
			return nil, errors.Wrapf(err, "no running task found")
		}
		return nil, err
	}
	var i IO
	if ioAttach != nil {
		// get the existing fifo paths from the task information stored by the daemon
		paths := &FIFOSet{
			Dir: getFifoDir([]string{
				response.Process.Stdin,
				response.Process.Stdout,
				response.Process.Stderr,
			}),
			In:       response.Process.Stdin,
			Out:      response.Process.Stdout,
			Err:      response.Process.Stderr,
			Terminal: response.Process.Terminal,
		}
		if i, err = ioAttach(paths); err != nil {
			return nil, err
		}
	}
	t := &task{
		client: c.client,
		io:     i,
		id:     response.Process.ID,
		pid:    response.Process.Pid,
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

package containerd

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/mount"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type Container interface {
	ID() string
	Delete(context.Context) error
	NewTask(context.Context, IOCreation) (Task, error)
	Spec() (*specs.Spec, error)
	Task() Task
	LoadTask(context.Context, IOAttach) (Task, error)
}

func containerFromProto(client *Client, c containers.Container) *container {
	return &container{
		client: client,
		c:      c,
	}
}

var _ = (Container)(&container{})

type container struct {
	client *Client

	c    containers.Container
	task *task
}

// ID returns the container's unique id
func (c *container) ID() string {
	return c.c.ID
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

func (c *container) Task() Task {
	return c.task
}

func (c *container) NewTask(ctx context.Context, ioCreate IOCreation) (Task, error) {
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
	response, err := c.client.TaskService().Create(ctx, request)
	if err != nil {
		return nil, err
	}
	t := &task{
		client:      c.client,
		io:          i,
		containerID: response.ContainerID,
		pid:         response.Pid,
	}
	c.task = t
	return t, nil
}

func (c *container) LoadTask(ctx context.Context, ioAttach IOAttach) (Task, error) {
	response, err := c.client.TaskService().Info(ctx, &execution.InfoRequest{
		ContainerID: c.c.ID,
	})
	if err != nil {
		return nil, err
	}
	// get the existing fifo paths from the task information stored by the daemon
	paths := &FifoSet{
		Dir: getFifoDir([]string{
			response.Task.Stdin,
			response.Task.Stdout,
			response.Task.Stderr,
		}),
		In:  response.Task.Stdin,
		Out: response.Task.Stdout,
		Err: response.Task.Stderr,
	}
	i, err := ioAttach(paths)
	if err != nil {
		return nil, err
	}
	t := &task{
		client:      c.client,
		io:          i,
		containerID: response.Task.ContainerID,
		pid:         response.Task.Pid,
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

package containerd

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/mount"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func containerFromProto(client *Client, c containers.Container) *Container {
	return &Container{
		client: client,
		c:      c,
	}
}

type Container struct {
	client *Client

	c    containers.Container
	task *Task
}

// ID returns the container's unique id
func (c *Container) ID() string {
	return c.c.ID
}

// Spec returns the current OCI specification for the container
func (c *Container) Spec() (*specs.Spec, error) {
	var s specs.Spec
	if err := json.Unmarshal(c.c.Spec.Value, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Delete deletes an existing container
// an error is returned if the container has running tasks
func (c *Container) Delete(ctx context.Context) error {
	// TODO: should the client be the one removing resources attached
	// to the container at the moment before we have GC?
	err := c.client.snapshotter().Remove(ctx, c.c.RootFS)

	if _, cerr := c.client.containers().Delete(ctx, &containers.DeleteContainerRequest{
		ID: c.c.ID,
	}); err == nil {
		err = cerr
	}
	return err
}

func (c *Container) Task() *Task {
	return c.task
}

func (c *Container) NewTask(ctx context.Context, ioCreate IOCreation) (*Task, error) {
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
	// get the rootfs from the snapshotter and add it to the request
	mounts, err := c.client.snapshotter().Mounts(ctx, c.c.RootFS)
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
	response, err := c.client.tasks().Create(ctx, request)
	if err != nil {
		return nil, err
	}
	t := &Task{
		client:      c.client,
		io:          i,
		containerID: response.ContainerID,
		pid:         response.Pid,
	}
	c.task = t
	return t, nil
}

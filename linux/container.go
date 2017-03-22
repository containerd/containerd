package linux

import (
	"github.com/docker/containerd"
	"github.com/docker/containerd/api/services/shim"
	"github.com/docker/containerd/api/types/container"
	"golang.org/x/net/context"
)

type State struct {
	pid    uint32
	status containerd.ContainerStatus
}

func (s State) Pid() uint32 {
	return s.pid
}

func (s State) Status() containerd.ContainerStatus {
	return s.status
}

type Container struct {
	id string

	shim shim.ShimClient
}

func (c *Container) Info() containerd.ContainerInfo {
	return containerd.ContainerInfo{
		ID:      c.id,
		Runtime: runtimeName,
	}
}

func (c *Container) Start(ctx context.Context) error {
	_, err := c.shim.Start(ctx, &shim.StartRequest{})
	return err
}

func (c *Container) State(ctx context.Context) (containerd.State, error) {
	response, err := c.shim.State(ctx, &shim.StateRequest{})
	if err != nil {
		return nil, err
	}
	var status containerd.ContainerStatus
	switch response.Status {
	case container.Status_CREATED:
		status = containerd.CreatedStatus
	case container.Status_RUNNING:
		status = containerd.RunningStatus
	case container.Status_STOPPED:
		status = containerd.StoppedStatus
	case container.Status_PAUSED:
		status = containerd.PausedStatus
		// TODO: containerd.DeletedStatus
	}
	return &State{
		pid:    response.Pid,
		status: status,
	}, nil
}

func (c *Container) Pause(ctx context.Context) error {
	_, err := c.shim.Pause(ctx, &shim.PauseRequest{})
	return err
}

func (c *Container) Resume(ctx context.Context) error {
	_, err := c.shim.Resume(ctx, &shim.ResumeRequest{})
	return err
}

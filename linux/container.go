package main

import (
	"github.com/docker/containerd"
	"github.com/docker/containerd/api/services/shim"
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
	return &State{
		pid: response.Pid,
	}, nil
}

package containerd

import "github.com/containerd/containerd/api/services/containers"

func containerFromProto(client *Client, c containers.Container) *Container {
	return &Container{
		client: client,
		id:     c.ID,
	}
}

type Container struct {
	client *Client

	id string
}

// ID returns the container's unique id
func (c *Container) ID() string {
	return c.id
}

package containerd

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/api/services/containers"
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

	c containers.Container
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
	_, err := c.client.containers().Delete(ctx, &containers.DeleteContainerRequest{
		ID: c.c.ID,
	})
	return err
}

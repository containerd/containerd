package plugin

import (
	"context"
	"time"

	"github.com/containerd/containerd"
)

type IO struct {
	Stdin    string
	Stdout   string
	Stderr   string
	Terminal bool
}

type CreateOpts struct {
	// Spec is the OCI runtime spec
	Spec []byte
	// Rootfs mounts to perform to gain access to the container's filesystem
	Rootfs []containerd.Mount
	// IO for the container's main process
	IO IO
}

type Exit struct {
	Status    uint32
	Timestamp time.Time
}

// Runtime is responsible for the creation of containers for a certain platform,
// arch, or custom usage.
type Runtime interface {
	// Create creates a container with the provided id and options
	Create(ctx context.Context, id string, opts CreateOpts) (Container, error)
	// Containers returns all the current containers for the runtime
	Containers(context.Context) ([]Container, error)
	// Delete removes the container in the runtime
	Delete(context.Context, Container) (*Exit, error)
	// Events returns events for the runtime and all containers created by the runtime
	Events(context.Context) <-chan *containerd.Event
}

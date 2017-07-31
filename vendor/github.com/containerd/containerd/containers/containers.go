package containers

import (
	"context"
	"time"

	"github.com/gogo/protobuf/types"
)

// Container represents the set of data pinned by a container. Unless otherwise
// noted, the resources here are considered in use by the container.
//
// The resources specified in this object are used to create tasks from the container.
type Container struct {
	ID          string
	Labels      map[string]string
	Image       string
	Runtime     RuntimeInfo
	Spec        *types.Any
	RootFS      string
	Snapshotter string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RuntimeInfo struct {
	Name    string
	Options *types.Any
}

type Store interface {
	Get(ctx context.Context, id string) (Container, error)

	// List returns containers that match one or more of the provided filters.
	List(ctx context.Context, filters ...string) ([]Container, error)

	Create(ctx context.Context, container Container) (Container, error)

	// Update the container with the provided container object. ID must be set.
	//
	// If one or more fieldpaths are provided, only the field corresponding to
	// the fieldpaths will be mutated.
	Update(ctx context.Context, container Container, fieldpaths ...string) (Container, error)

	Delete(ctx context.Context, id string) error
}

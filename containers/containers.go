package containers

import "context"

// Container represents the set of data pinned by a container. Unless otherwise
// noted, the resources here are considered in use by the container.
//
// The resources specified in this object are used to create tasks from the container.
type Container struct {
	ID      string
	Labels  map[string]string
	Image   string
	Runtime string
	Spec    []byte
	RootFS  string
}

type Store interface {
	Get(ctx context.Context, id string) (Container, error)
	List(ctx context.Context, filter string) ([]Container, error)
	Create(ctx context.Context, container Container) (Container, error)
	Update(ctx context.Context, container Container) (Container, error)
	Delete(ctx context.Context, id string) error
}

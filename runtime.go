package containerd

import (
	"fmt"
	"sync"

	"golang.org/x/net/context"
)

// NewRuntimeFunc is the runtime's constructor
type NewRuntimeFunc func(root string) (Runtime, error)

var runtimeRegistration = struct {
	mu       sync.Mutex
	runtimes map[string]NewRuntimeFunc
}{
	runtimes: make(map[string]NewRuntimeFunc),
}

// RegisterRuntime is not external packages registers Runtimes for use with containerd
func RegisterRuntime(name string, f NewRuntimeFunc) {
	runtimeRegistration.mu.Lock()
	defer runtimeRegistration.mu.Unlock()
	if _, ok := runtimeRegistration.runtimes[name]; ok {
		panic(fmt.Errorf("runtime already registered as %q", name))
	}
	runtimeRegistration.runtimes[name] = f
}

// Runtimes returns a slice of all registered runtime names for containerd
func Runtimes() (o []string) {
	runtimeRegistration.mu.Lock()
	defer runtimeRegistration.mu.Unlock()

	for k := range runtimeRegistration.runtimes {
		o = append(o, k)
	}
	return o
}

// NewRuntime calls the runtime's constructor with the provided root
func NewRuntime(name, root string) (Runtime, error) {
	runtimeRegistration.mu.Lock()
	defer runtimeRegistration.mu.Unlock()
	f, ok := runtimeRegistration.runtimes[name]
	if !ok {
		return nil, ErrRuntimeNotExist
	}
	return f(root)
}

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
	Rootfs []Mount
	// IO for the container's main process
	IO IO
}

// Runtime is responsible for the creation of containers for a certain platform,
// arch, or custom usage.
type Runtime interface {
	// Create creates a container with the provided id and options
	Create(ctx context.Context, id string, opts CreateOpts) (Container, error)
	// Containers returns all the current containers for the runtime
	Containers() ([]Container, error)
	// Delete returns the container in the runtime
	Delete(ctx context.Context, c Container) error
	// Events returns events for the runtime and all containers created by the runtime
	Events(context.Context) <-chan *Event
}

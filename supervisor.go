package containerd

import (
	"sync"

	"golang.org/x/net/context"
)

func NewSupervisor(ctx context.Context, runtimes map[string]Runtime) (*Supervisor, error) {
	c, err := newCollector(ctx, runtimes)
	if err != nil {
		return nil, err
	}
	s := &Supervisor{
		containers: make(map[string]Container),
		runtimes:   runtimes,
		collector:  c,
	}
	for _, r := range runtimes {
		containers, err := r.Containers()
		if err != nil {
			return nil, err
		}
		for _, c := range containers {
			s.containers[c.Info().ID] = c
		}
	}
	return s, nil
}

// Supervisor supervises containers and events from multiple runtimes
type Supervisor struct {
	mu sync.Mutex

	containers map[string]Container
	runtimes   map[string]Runtime
	collector  *collector
}

// ForwardEvents is a blocking method that will forward all events from the supervisor
// to the EventWriter provided by the caller
func (s *Supervisor) ForwardEvents(w EventWriter) error {
	return s.collector.forward(w)
}

// Create creates a new container with the provided runtime
func (s *Supervisor) Create(ctx context.Context, id, runtime string, opts CreateOpts) (Container, error) {
	r, ok := s.runtimes[runtime]
	if !ok {
		return nil, ErrUnknownRuntime
	}
	// check to make sure the container's id is unique across the entire system
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.containers[id]; ok {
		return nil, ErrContainerExists
	}
	c, err := r.Create(ctx, id, opts)
	if err != nil {
		return nil, err
	}
	s.containers[c.Info().ID] = c
	return c, nil
}

// Delete deletes the container
func (s *Supervisor) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.containers[id]
	if !ok {
		return ErrContainerNotExist
	}
	err := s.runtimes[c.Info().Runtime].Delete(ctx, c)
	if err != nil {
		return err
	}
	delete(s.containers, id)
	return nil
}

// Containers returns all the containers for the supervisor
func (s *Supervisor) Containers() (o []Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.containers {
		o = append(o, c)
	}
	return o
}

func (s *Supervisor) Get(id string) (Container, error) {
	s.mu.Lock()
	c, ok := s.containers[id]
	s.mu.Unlock()
	if !ok {
		return nil, ErrContainerNotExist
	}
	return c, nil
}

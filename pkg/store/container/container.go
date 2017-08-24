/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package container

import (
	"sync"

	"github.com/containerd/containerd"

	cio "github.com/kubernetes-incubator/cri-containerd/pkg/server/io"
	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

// Container contains all resources associated with the container. All methods to
// mutate the internal state are thread-safe.
type Container struct {
	// Metadata is the metadata of the container, it is **immutable** after created.
	Metadata
	// Status stores the status of the container.
	Status StatusStorage
	// Containerd container
	Container containerd.Container
	// Container IO
	IO *cio.ContainerIO
	// TODO(random-liu): Add stop channel to get rid of stop poll waiting.
}

// Opts sets specific information to newly created Container.
type Opts func(*Container)

// WithContainer adds the containerd Container to the internal data store.
func WithContainer(cntr containerd.Container) Opts {
	return func(c *Container) {
		c.Container = cntr
	}
}

// WithContainerIO adds IO into the container.
func WithContainerIO(io *cio.ContainerIO) Opts {
	return func(c *Container) {
		c.IO = io
	}
}

// NewContainer creates an internally used container type.
func NewContainer(metadata Metadata, status Status, opts ...Opts) (Container, error) {
	s, err := StoreStatus(metadata.ID, status)
	if err != nil {
		return Container{}, err
	}
	c := Container{
		Metadata: metadata,
		Status:   s,
	}
	for _, o := range opts {
		o(&c)
	}
	return c, nil
}

// Delete deletes checkpoint for the container.
func (c *Container) Delete() error {
	return c.Status.Delete()
}

// LoadContainer loads the internal used container type.
func LoadContainer() (Container, error) {
	return Container{}, nil
}

// Store stores all Containers.
type Store struct {
	lock       sync.RWMutex
	containers map[string]Container
	// TODO(random-liu): Add trunc index.
}

// LoadStore loads containers from runtime.
// TODO(random-liu): Implement LoadStore.
func LoadStore() *Store { return nil }

// NewStore creates a container store.
func NewStore() *Store {
	return &Store{containers: make(map[string]Container)}
}

// Add a container into the store. Returns store.ErrAlreadyExist if the
// container already exists.
func (s *Store) Add(c Container) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.containers[c.ID]; ok {
		return store.ErrAlreadyExist
	}
	s.containers[c.ID] = c
	return nil
}

// Get returns the container with specified id. Returns store.ErrNotExist
// if the container doesn't exist.
func (s *Store) Get(id string) (Container, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if c, ok := s.containers[id]; ok {
		return c, nil
	}
	return Container{}, store.ErrNotExist
}

// List lists all containers.
func (s *Store) List() []Container {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var containers []Container
	for _, c := range s.containers {
		containers = append(containers, c)
	}
	return containers
}

// Delete deletes the container from store with specified id.
func (s *Store) Delete(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.containers, id)
}

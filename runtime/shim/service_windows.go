// +build windows

/*
   Copyright The containerd Authors.

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

package shim

import (
	"context"
	"fmt"
	"os"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	rproc "github.com/containerd/containerd/runtime/proc"
	shimapi "github.com/containerd/containerd/runtime/shim/v1"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Config contains shim specific configuration
type Config struct {
	Path        string
	Namespace   string
	RuntimeRoot string
}

// NewService returns a new shim service that can be used via GRPC
func NewService(config Config, publisher events.Publisher) (*Service, error) {
	if config.Namespace == "" {
		return nil, fmt.Errorf("shim namespace cannot be empty")
	}
	ctx := namespaces.WithNamespace(context.Background(), config.Namespace)
	ctx = log.WithLogger(ctx, logrus.WithFields(logrus.Fields{
		"namespace": config.Namespace,
		"path":      config.Path,
		"pid":       os.Getpid(),
	}))
	s := &Service{
		config:    config,
		context:   ctx,
		processes: make(map[string]rproc.Process),
		events:    make(chan interface{}, 128),
		// TODO: JTERRY75 - Add a windows monitor
		// ec:        Default.Subscribe(),
	}
	/*
		// TODO: JTERRY75 - handle exits for windows
		go s.processExits()
	*/
	/*
		// TODO: JTERRY75 - Windows specific init here.
		if err := s.initPlatform(); err != nil {
			return nil, errors.Wrap(err, "failed to initialized platform behavior")
		}
	*/
	go s.forward(publisher)
	return s, nil
}

// Create a new initial process and container with the underlying OCI runtime
func (s *Service) Create(ctx context.Context, r *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// TODO: JTERRY75 - Implement windows/proc
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

// Exec an additional process inside the container
func (s *Service) Exec(ctx context.Context, r *shimapi.ExecProcessRequest) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p := s.processes[r.ID]; p != nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrAlreadyExists, "id %s", r.ID)
	}

	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

// Pause the container
func (s *Service) Pause(ctx context.Context, r *ptypes.Empty) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

// Resume the container
func (s *Service) Resume(ctx context.Context, r *ptypes.Empty) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

// Checkpoint the container
func (s *Service) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

// Update a running container
func (s *Service) Update(ctx context.Context, r *shimapi.UpdateTaskRequest) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

func (s *Service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.processes[s.id]
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	// TODO: JTERRY75
	return nil, errdefs.ToGRPC(errors.New("not implemented"))
}

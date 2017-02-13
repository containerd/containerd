package execution

import (
	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/services/execution"
	"github.com/docker/containerd/api/types/container"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
)

var (
	_     = (api.ContainerServiceServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

// New creates a new GRPC service for the ContainerService
func New(s *containerd.Supervisor) *Service {
	return &Service{
		s: s,
	}
}

type Service struct {
	s *containerd.Supervisor
}

func (s *Service) Create(ctx context.Context, r *api.CreateRequest) (*api.CreateResponse, error) {
	opts := containerd.CreateOpts{
		Spec: r.Spec.Value,
		IO: containerd.IO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
	}
	for _, m := range r.Rootfs {
		opts.Rootfs = append(opts.Rootfs, containerd.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	c, err := s.s.Create(ctx, r.ID, r.Runtime, opts)
	if err != nil {
		return nil, err
	}
	state, err := c.State(ctx)
	if err != nil {
		return nil, err
	}
	return &api.CreateResponse{
		ID:  r.ID,
		Pid: state.Pid(),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *api.StartRequest) (*google_protobuf.Empty, error) {
	c, err := s.s.Get(r.ID)
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *api.DeleteRequest) (*google_protobuf.Empty, error) {
	if err := s.s.Delete(ctx, r.ID); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) List(ctx context.Context, r *api.ListRequest) (*api.ListResponse, error) {
	resp := &api.ListResponse{}
	for _, c := range s.s.Containers() {
		state, err := c.State(ctx)
		if err != nil {
			return nil, err
		}
		var status container.Status
		switch state.Status() {
		case containerd.CreatedStatus:
			status = container.Status_CREATED
		case containerd.RunningStatus:
			status = container.Status_RUNNING
		case containerd.StoppedStatus:
			status = container.Status_STOPPED
		case containerd.PausedStatus:
			status = container.Status_PAUSED
		}
		resp.Containers = append(resp.Containers, &container.Container{
			ID:     c.ID(),
			Pid:    state.Pid(),
			Status: status,
		})
	}
	return resp, nil
}

func (s *Service) Events(r *api.EventsRequest, server api.ContainerService_EventsServer) error {
	w := &grpcEventWriter{
		server: server,
	}
	return s.s.ForwardEvents(w)
}

type grpcEventWriter struct {
	server api.ContainerService_EventsServer
}

func (g *grpcEventWriter) Write(e *containerd.Event) error {
	var t container.Event_EventType
	switch e.Type {
	case containerd.ExitEvent:
		t = container.Event_EXIT
	case containerd.ExecAddEvent:
		t = container.Event_EXEC_ADDED
	case containerd.PausedEvent:
		t = container.Event_PAUSED
	case containerd.CreateEvent:
		t = container.Event_CREATE
	case containerd.StartEvent:
		t = container.Event_START
	case containerd.OOMEvent:
		t = container.Event_OOM
	}
	return g.server.Send(&container.Event{
		Type:       t,
		ID:         e.ID,
		Pid:        e.Pid,
		ExitStatus: e.ExitStatus,
	})
}

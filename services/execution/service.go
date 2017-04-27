package execution

import (
	"sync"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/container"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	_     = (api.ContainerServiceServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

func init() {
	plugin.Register("runtime-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	c, err := newCollector(ic.Context, ic.Runtimes)
	if err != nil {
		return nil, err
	}
	return &Service{
		runtimes:   ic.Runtimes,
		containers: make(map[string]containerd.Container),
		collector:  c,
	}, nil
}

type Service struct {
	mu sync.Mutex

	runtimes   map[string]containerd.Runtime
	containers map[string]containerd.Container
	collector  *collector
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterContainerServiceServer(server, s)
	// load all containers
	for _, r := range s.runtimes {
		containers, err := r.Containers(context.Background())
		if err != nil {
			return err
		}
		for _, c := range containers {
			s.containers[c.Info().ID] = c
		}
	}
	return nil
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
	runtime, err := s.getRuntime(r.Runtime)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if _, ok := s.containers[r.ID]; ok {
		s.mu.Unlock()
		return nil, containerd.ErrContainerExists
	}
	c, err := runtime.Create(ctx, r.ID, opts)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.containers[r.ID] = c
	s.mu.Unlock()
	state, err := c.State(ctx)
	if err != nil {
		s.mu.Lock()
		delete(s.containers, r.ID)
		runtime.Delete(ctx, c)
		s.mu.Unlock()
		return nil, err
	}
	return &api.CreateResponse{
		ID:  r.ID,
		Pid: state.Pid(),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *api.StartRequest) (*google_protobuf.Empty, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *api.DeleteRequest) (*api.DeleteResponse, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	runtime, err := s.getRuntime(c.Info().Runtime)
	if err != nil {
		return nil, err
	}
	exit, err := runtime.Delete(ctx, c)
	if err != nil {
		return nil, err
	}

	delete(s.containers, r.ID)

	return &api.DeleteResponse{
		ExitStatus: exit.Status,
		ExitedAt:   exit.Timestamp,
	}, nil
}

func containerFromContainerd(ctx context.Context, c containerd.Container) (*container.Container, error) {
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
	default:
		log.G(ctx).WithField("status", state.Status()).Warn("unknown status")
	}
	return &container.Container{
		ID:     c.Info().ID,
		Pid:    state.Pid(),
		Status: status,
	}, nil
}

func (s *Service) Info(ctx context.Context, r *api.InfoRequest) (*container.Container, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	return containerFromContainerd(ctx, c)
}

func (s *Service) List(ctx context.Context, r *api.ListRequest) (*api.ListResponse, error) {
	resp := &api.ListResponse{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cd := range s.containers {
		c, err := containerFromContainerd(ctx, cd)
		if err != nil {
			return nil, err
		}
		resp.Containers = append(resp.Containers, c)
	}
	return resp, nil
}

func (s *Service) Kill(ctx context.Context, r *api.KillRequest) (*google_protobuf.Empty, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := c.Kill(ctx, r.Signal, r.All); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Events(r *api.EventsRequest, server api.ContainerService_EventsServer) error {
	w := &grpcEventWriter{
		server: server,
	}
	return s.collector.forward(w)
}

func (s *Service) Exec(ctx context.Context, r *api.ExecRequest) (*api.ExecResponse, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	process, err := c.Exec(ctx, containerd.ExecOpts{
		Spec: r.Spec.Value,
		IO: containerd.IO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
	})
	if err != nil {
		return nil, err
	}
	state, err := process.State(ctx)
	if err != nil {
		return nil, err
	}
	return &api.ExecResponse{
		Pid: state.Pid(),
	}, nil
}

func (s *Service) Pty(ctx context.Context, r *api.PtyRequest) (*google_protobuf.Empty, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := c.Pty(ctx, r.Pid, containerd.ConsoleSize{
		Width:  r.Width,
		Height: r.Height,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) CloseStdin(ctx context.Context, r *api.CloseStdinRequest) (*google_protobuf.Empty, error) {
	c, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := c.CloseStdin(ctx, r.Pid); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) getContainer(id string) (containerd.Container, error) {
	s.mu.Lock()
	c, ok := s.containers[id]
	s.mu.Unlock()
	if !ok {
		return nil, containerd.ErrContainerNotExist
	}
	return c, nil
}

func (s *Service) getRuntime(name string) (containerd.Runtime, error) {
	runtime, ok := s.runtimes[name]
	if !ok {
		return nil, containerd.ErrUnknownRuntime
	}
	return runtime, nil
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

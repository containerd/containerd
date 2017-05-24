package execution

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/boltdb/bolt"
	api "github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/descriptor"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	protobuf "github.com/gogo/protobuf/types"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	specs "github.com/opencontainers/image-spec/specs-go"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	_     = (api.TasksServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

func init() {
	plugin.Register("tasks-grpc", &plugin.Registration{
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
		runtimes:  ic.Runtimes,
		tasks:     make(map[string]plugin.Task),
		db:        ic.Meta,
		collector: c,
		store:     ic.Content,
	}, nil
}

type Service struct {
	mu sync.Mutex

	runtimes  map[string]plugin.Runtime
	tasks     map[string]plugin.Task
	db        *bolt.DB
	collector *collector
	store     content.Store
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterTasksServer(server, s)
	// load all tasks
	for _, r := range s.runtimes {
		tasks, err := r.Tasks(context.Background())
		if err != nil {
			return err
		}
		for _, c := range tasks {
			s.tasks[c.Info().ContainerID] = c
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, r *api.CreateRequest) (*api.CreateResponse, error) {
	var (
		checkpointPath string
		err            error
	)
	if r.Checkpoint != nil {
		checkpointPath, err = ioutil.TempDir("", "ctrd-checkpoint")
		if err != nil {
			return nil, err
		}
		if r.Checkpoint.MediaType != images.MediaTypeContainerd1Checkpoint {
			return nil, fmt.Errorf("unsupported checkpoint type %q", r.Checkpoint.MediaType)
		}
		reader, err := s.store.Reader(ctx, r.Checkpoint.Digest)
		if err != nil {
			return nil, err
		}
		_, err = archive.Apply(ctx, checkpointPath, reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
	}

	var container containers.Container
	if err := s.db.View(func(tx *bolt.Tx) error {
		store := containers.NewStore(tx)
		var err error
		container, err = store.Get(ctx, r.ContainerID)
		return err
	}); err != nil {
		switch {
		case containers.IsNotFound(err):
			return nil, grpc.Errorf(codes.NotFound, "container %v not found", r.ContainerID)
		case containers.IsExists(err):
			return nil, grpc.Errorf(codes.AlreadyExists, "container %v already exists", r.ContainerID)
		}

		return nil, err
	}

	opts := plugin.CreateOpts{
		Spec: container.Spec,
		IO: plugin.IO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		Checkpoint: checkpointPath,
	}
	for _, m := range r.Rootfs {
		opts.Rootfs = append(opts.Rootfs, mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	runtime, err := s.getRuntime(container.Runtime)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if _, ok := s.tasks[r.ContainerID]; ok {
		s.mu.Unlock()
		return nil, grpc.Errorf(codes.AlreadyExists, "task %v already exists", r.ContainerID)
	}
	c, err := runtime.Create(ctx, r.ContainerID, opts)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.tasks[r.ContainerID] = c
	s.mu.Unlock()
	state, err := c.State(ctx)
	if err != nil {
		s.mu.Lock()
		delete(s.tasks, r.ContainerID)
		runtime.Delete(ctx, c)
		s.mu.Unlock()
		return nil, err
	}
	return &api.CreateResponse{
		ContainerID: r.ContainerID,
		Pid:         state.Pid(),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *api.StartRequest) (*google_protobuf.Empty, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *api.DeleteRequest) (*api.DeleteResponse, error) {
	c, err := s.getTask(r.ContainerID)
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

	delete(s.tasks, r.ContainerID)

	return &api.DeleteResponse{
		ExitStatus: exit.Status,
		ExitedAt:   exit.Timestamp,
	}, nil
}

func taskFromContainerd(ctx context.Context, c plugin.Task) (*task.Task, error) {
	state, err := c.State(ctx)
	if err != nil {
		return nil, err
	}

	var status task.Status
	switch state.Status() {
	case plugin.CreatedStatus:
		status = task.StatusCreated
	case plugin.RunningStatus:
		status = task.StatusRunning
	case plugin.StoppedStatus:
		status = task.StatusStopped
	case plugin.PausedStatus:
		status = task.StatusPaused
	default:
		log.G(ctx).WithField("status", state.Status()).Warn("unknown status")
	}
	return &task.Task{
		ID:     c.Info().ID,
		Pid:    state.Pid(),
		Status: status,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   c.Info().Spec,
		},
	}, nil
}

func (s *Service) Info(ctx context.Context, r *api.InfoRequest) (*api.InfoResponse, error) {
	task, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	t, err := taskFromContainerd(ctx, task)
	if err != nil {
		return nil, err
	}
	return &api.InfoResponse{
		Task: t,
	}, nil
}

func (s *Service) List(ctx context.Context, r *api.ListRequest) (*api.ListResponse, error) {
	resp := &api.ListResponse{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cd := range s.tasks {
		c, err := taskFromContainerd(ctx, cd)
		if err != nil {
			return nil, err
		}
		resp.Tasks = append(resp.Tasks, c)
	}
	return resp, nil
}

func (s *Service) Pause(ctx context.Context, r *api.PauseRequest) (*google_protobuf.Empty, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	err = c.Pause(ctx)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *api.ResumeRequest) (*google_protobuf.Empty, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	err = c.Resume(ctx)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *api.KillRequest) (*google_protobuf.Empty, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}

	switch v := r.PidOrAll.(type) {
	case *api.KillRequest_All:
		if err := c.Kill(ctx, r.Signal, 0, true); err != nil {
			return nil, err
		}
	case *api.KillRequest_Pid:
		if err := c.Kill(ctx, r.Signal, v.Pid, false); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid option specified; expected pid or all")
	}
	return empty, nil
}

func (s *Service) Processes(ctx context.Context, r *api.ProcessesRequest) (*api.ProcessesResponse, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}

	pids, err := c.Processes(ctx)
	if err != nil {
		return nil, err
	}

	ps := []*task.Process{}
	for _, pid := range pids {
		ps = append(ps, &task.Process{
			Pid: pid,
		})
	}

	resp := &api.ProcessesResponse{
		Processes: ps,
	}

	return resp, nil
}

func (s *Service) Events(r *api.EventsRequest, server api.Tasks_EventsServer) error {
	w := &grpcEventWriter{
		server: server,
	}
	return s.collector.forward(w)
}

func (s *Service) Exec(ctx context.Context, r *api.ExecRequest) (*api.ExecResponse, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	process, err := c.Exec(ctx, plugin.ExecOpts{
		Spec: r.Spec.Value,
		IO: plugin.IO{
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
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	if err := c.Pty(ctx, r.Pid, plugin.ConsoleSize{
		Width:  r.Width,
		Height: r.Height,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) CloseStdin(ctx context.Context, r *api.CloseStdinRequest) (*google_protobuf.Empty, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	if err := c.CloseStdin(ctx, r.Pid); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Checkpoint(ctx context.Context, r *api.CheckpointRequest) (*api.CheckpointResponse, error) {
	c, err := s.getTask(r.ContainerID)
	if err != nil {
		return nil, err
	}
	image, err := ioutil.TempDir("", "ctd-checkpoint")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(image)
	if err := c.Checkpoint(ctx, plugin.CheckpointOpts{
		Exit:             r.Exit,
		AllowTCP:         r.AllowTcp,
		AllowTerminal:    r.AllowTerminal,
		AllowUnixSockets: r.AllowUnixSockets,
		FileLocks:        r.FileLocks,
		// ParentImage: r.ParentImage,
		EmptyNamespaces: r.EmptyNamespaces,
		Path:            image,
	}); err != nil {
		return nil, err
	}
	// write checkpoint to the content store
	tar := archive.Diff(ctx, "", image)
	cp, err := s.writeContent(ctx, images.MediaTypeContainerd1Checkpoint, image, tar)
	// close tar first after write
	if err := tar.Close(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	// write the config to the content store
	spec := bytes.NewReader(c.Info().Spec)
	specD, err := s.writeContent(ctx, images.MediaTypeContainerd1CheckpointConfig, filepath.Join(image, "spec"), spec)
	if err != nil {
		return nil, err
	}
	return &api.CheckpointResponse{
		Descriptors: []*descriptor.Descriptor{
			cp,
			specD,
		},
	}, nil
}

func (s *Service) writeContent(ctx context.Context, mediaType, ref string, r io.Reader) (*descriptor.Descriptor, error) {
	writer, err := s.store.Writer(ctx, ref, 0, "")
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	size, err := io.Copy(writer, r)
	if err != nil {
		return nil, err
	}
	if err := writer.Commit(0, ""); err != nil {
		return nil, err
	}
	return &descriptor.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size_:     size,
	}, nil
}

func (s *Service) getTask(id string) (plugin.Task, error) {
	s.mu.Lock()
	c, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return nil, grpc.Errorf(codes.NotFound, "task %v not found", id)
	}
	return c, nil
}

func (s *Service) getRuntime(name string) (plugin.Runtime, error) {
	runtime, ok := s.runtimes[name]
	if !ok {
		return nil, plugin.ErrUnknownRuntime
	}
	return runtime, nil
}

type grpcEventWriter struct {
	server api.Tasks_EventsServer
}

func (g *grpcEventWriter) Write(e *plugin.Event) error {
	var t task.Event_EventType
	switch e.Type {
	case plugin.ExitEvent:
		t = task.Event_EXIT
	case plugin.ExecAddEvent:
		t = task.Event_EXEC_ADDED
	case plugin.PausedEvent:
		t = task.Event_PAUSED
	case plugin.CreateEvent:
		t = task.Event_CREATE
	case plugin.StartEvent:
		t = task.Event_START
	case plugin.OOMEvent:
		t = task.Event_OOM
	}
	return g.server.Send(&task.Event{
		Type:       t,
		ID:         e.ID,
		Pid:        e.Pid,
		ExitStatus: e.ExitStatus,
	})
}

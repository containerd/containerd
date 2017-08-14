// +build !windows

package shim

import (
	"fmt"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/console"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/runtime"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var empty = &google_protobuf.Empty{}

const RuncRoot = "/run/containerd/runc"

// NewService returns a new shim service that can be used via GRPC
func NewService(path, namespace, workDir string, publisher events.Publisher) (*Service, error) {
	if namespace == "" {
		return nil, fmt.Errorf("shim namespace cannot be empty")
	}
	context := namespaces.WithNamespace(context.Background(), namespace)
	s := &Service{
		path:      path,
		processes: make(map[string]process),
		events:    make(chan interface{}, 4096),
		namespace: namespace,
		context:   context,
		workDir:   workDir,
	}
	if err := s.initPlatform(); err != nil {
		return nil, errors.Wrap(err, "failed to initialized platform behavior")
	}
	go s.forward(publisher)
	return s, nil
}

// platform handles platform-specific behavior that may differs across
// platform implementations
type platform interface {
	copyConsole(ctx context.Context, console console.Console, stdin, stdout, stderr string, wg, cwg *sync.WaitGroup) (console.Console, error)
	shutdownConsole(ctx context.Context, console console.Console) error
}

type Service struct {
	path          string
	id            string
	bundle        string
	mu            sync.Mutex
	processes     map[string]process
	events        chan interface{}
	eventsMu      sync.Mutex
	deferredEvent interface{}
	namespace     string
	context       context.Context

	workDir  string
	platform platform
}

func (s *Service) Create(ctx context.Context, r *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	process, err := newInitProcess(ctx, s.platform, s.path, s.namespace, s.workDir, r)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.mu.Lock()
	// save the main task id and bundle to the shim for additional requests
	s.id = r.ID
	s.bundle = r.Bundle
	pid := process.Pid()
	s.processes[r.ID] = process
	s.mu.Unlock()
	cmd := &reaper.Cmd{
		ExitCh: make(chan struct{}),
	}
	reaper.Default.Register(pid, cmd)
	s.events <- &eventsapi.TaskCreate{
		ContainerID: r.ID,
		Bundle:      r.Bundle,
		Rootfs:      r.Rootfs,
		IO: &eventsapi.TaskIO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		Checkpoint: r.Checkpoint,
		Pid:        uint32(pid),
	}
	go s.waitExit(process, pid, cmd)
	return &shimapi.CreateTaskResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *shimapi.StartRequest) (*shimapi.StartResponse, error) {
	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process %s not found", r.ID)
	}
	if err := p.Start(ctx); err != nil {
		return nil, err
	}
	if r.ID == s.id {
		s.events <- &eventsapi.TaskStart{
			ContainerID: s.id,
			Pid:         uint32(p.Pid()),
		}
	} else {
		pid := p.Pid()
		cmd := &reaper.Cmd{
			ExitCh: make(chan struct{}),
		}
		reaper.Default.Register(pid, cmd)
		go s.waitExit(p, pid, cmd)
		s.events <- &eventsapi.TaskExecStarted{
			ContainerID: s.id,
			ExecID:      r.ID,
			Pid:         uint32(pid),
		}
	}
	return &shimapi.StartResponse{
		ID:  p.ID(),
		Pid: uint32(p.Pid()),
	}, nil
}

func (s *Service) Delete(ctx context.Context, r *google_protobuf.Empty) (*shimapi.DeleteResponse, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	s.deleteProcess(p.ID())
	s.events <- &eventsapi.TaskDelete{
		ContainerID: s.id,
		ExitStatus:  uint32(p.ExitStatus()),
		ExitedAt:    p.ExitedAt(),
		Pid:         uint32(p.Pid()),
	}
	return &shimapi.DeleteResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
		Pid:        uint32(p.Pid()),
	}, nil
}

func (s *Service) DeleteProcess(ctx context.Context, r *shimapi.DeleteProcessRequest) (*shimapi.DeleteResponse, error) {
	if r.ID == s.id {
		return nil, grpc.Errorf(codes.InvalidArgument, "cannot delete init process with DeleteProcess")
	}
	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "process %s", r.ID)
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	s.deleteProcess(r.ID)
	return &shimapi.DeleteResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
		Pid:        uint32(p.Pid()),
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *shimapi.ExecProcessRequest) (*google_protobuf.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p := s.processes[r.ID]; p != nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrAlreadyExists, "id %s", r.ID)
	}

	p := s.processes[s.id]
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	process, err := newExecProcess(ctx, s.path, r, p.(*initProcess), r.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.processes[r.ID] = process

	s.events <- &eventsapi.TaskExecAdded{
		ContainerID: s.id,
		ExecID:      r.ID,
	}
	return empty, nil
}

func (s *Service) ResizePty(ctx context.Context, r *shimapi.ResizePtyRequest) (*google_protobuf.Empty, error) {
	if r.ID == "" {
		return nil, errdefs.ToGRPCf(errdefs.ErrInvalidArgument, "id not provided")
	}
	ws := console.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errors.Errorf("process does not exist %s", r.ID)
	}
	if err := p.Resize(ws); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

func (s *Service) State(ctx context.Context, r *shimapi.StateRequest) (*shimapi.StateResponse, error) {
	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process id %s not found", r.ID)
	}
	st, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	status := task.StatusUnknown
	switch st {
	case "created":
		status = task.StatusCreated
	case "running":
		status = task.StatusRunning
	case "stopped":
		status = task.StatusStopped
	case "paused":
		status = task.StatusPaused
	case "pausing":
		status = task.StatusPausing
	}
	sio := p.Stdio()
	return &shimapi.StateResponse{
		ID:         p.ID(),
		Bundle:     s.bundle,
		Pid:        uint32(p.Pid()),
		Status:     status,
		Stdin:      sio.stdin,
		Stdout:     sio.stdout,
		Stderr:     sio.stderr,
		Terminal:   sio.terminal,
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
	}, nil
}

func (s *Service) Pause(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := p.(*initProcess).Pause(ctx); err != nil {
		return nil, err
	}
	s.events <- &eventsapi.TaskPaused{
		ContainerID: s.id,
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := p.(*initProcess).Resume(ctx); err != nil {
		return nil, err
	}
	s.events <- &eventsapi.TaskResumed{
		ContainerID: s.id,
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *shimapi.KillRequest) (*google_protobuf.Empty, error) {
	if r.ID == "" {
		p := s.getProcess(s.id)
		if p == nil {
			return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
		}
		if err := p.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, errdefs.ToGRPC(err)
		}
		return empty, nil
	}

	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process id %s not found", r.ID)
	}
	if err := p.Kill(ctx, r.Signal, r.All); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

func (s *Service) ListPids(ctx context.Context, r *shimapi.ListPidsRequest) (*shimapi.ListPidsResponse, error) {
	pids, err := s.getContainerPids(ctx, r.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &shimapi.ListPidsResponse{
		Pids: pids,
	}, nil
}

func (s *Service) CloseIO(ctx context.Context, r *shimapi.CloseIORequest) (*google_protobuf.Empty, error) {
	p := s.getProcess(r.ID)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process does not exist %s", r.ID)
	}
	if err := p.Stdin().Close(); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) (*google_protobuf.Empty, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := p.(*initProcess).Checkpoint(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.events <- &eventsapi.TaskCheckpointed{
		ContainerID: s.id,
	}
	return empty, nil
}

func (s *Service) ShimInfo(ctx context.Context, r *google_protobuf.Empty) (*shimapi.ShimInfoResponse, error) {
	return &shimapi.ShimInfoResponse{
		ShimPid: uint32(os.Getpid()),
	}, nil
}

func (s *Service) Update(ctx context.Context, r *shimapi.UpdateTaskRequest) (*google_protobuf.Empty, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := p.(*initProcess).Update(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

func (s *Service) addProcess(id string, p process) {
	s.mu.Lock()
	s.processes[id] = p
	s.mu.Unlock()
}

func (s *Service) getProcess(id string) process {
	s.mu.Lock()
	p := s.processes[id]
	s.mu.Unlock()
	return p
}

func (s *Service) deleteProcess(id string) {
	s.mu.Lock()
	delete(s.processes, id)
	s.mu.Unlock()
}

func (s *Service) waitExit(p process, pid int, cmd *reaper.Cmd) {
	status, _ := reaper.Default.WaitPid(pid)
	p.SetExited(status)

	reaper.Default.Delete(pid)
	s.events <- &eventsapi.TaskExit{
		ContainerID: s.id,
		ID:          p.ID(),
		Pid:         uint32(pid),
		ExitStatus:  uint32(status),
		ExitedAt:    p.ExitedAt(),
	}
}

func (s *Service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	p := s.getProcess(s.id)
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "container must be created")
	}

	ps, err := p.(*initProcess).runtime.Ps(ctx, id)
	if err != nil {
		return nil, err
	}
	pids := make([]uint32, 0, len(ps))
	for _, pid := range ps {
		pids = append(pids, uint32(pid))
	}
	return pids, nil
}

func (s *Service) forward(publisher events.Publisher) {
	for e := range s.events {
		if err := publisher.Publish(s.context, getTopic(e), e); err != nil {
			log.G(s.context).WithError(err).Error("post event")
		}
	}
}

func getTopic(e interface{}) string {
	switch e.(type) {
	case *eventsapi.TaskCreate:
		return runtime.TaskCreateEventTopic
	case *eventsapi.TaskStart:
		return runtime.TaskStartEventTopic
	case *eventsapi.TaskOOM:
		return runtime.TaskOOMEventTopic
	case *eventsapi.TaskExit:
		return runtime.TaskExitEventTopic
	case *eventsapi.TaskDelete:
		return runtime.TaskDeleteEventTopic
	case *eventsapi.TaskExecAdded:
		return runtime.TaskExecAddedEventTopic
	case *eventsapi.TaskPaused:
		return runtime.TaskPausedEventTopic
	case *eventsapi.TaskResumed:
		return runtime.TaskResumedEventTopic
	case *eventsapi.TaskCheckpointed:
		return runtime.TaskCheckpointedEventTopic
	}
	return "?"
}

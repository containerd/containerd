// +build !windows

package shim

import (
	"fmt"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/console"
	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	evt "github.com/containerd/containerd/events"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var empty = &google_protobuf.Empty{}

const RuncRoot = "/run/containerd/runc"

// NewService returns a new shim service that can be used via GRPC
func NewService(path, namespace, address string) (*Service, error) {
	if namespace == "" {
		return nil, fmt.Errorf("shim namespace cannot be empty")
	}
	context := namespaces.WithNamespace(context.Background(), namespace)
	var client poster
	if address != "" {
		conn, err := connect(address, dialer)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to dial %q", address)
		}
		client = events.NewEventsClient(conn)
	} else {
		client = &localEventsClient{
			emitter: evt.GetPoster(context),
		}
	}
	s := &Service{
		path:      path,
		processes: make(map[string]process),
		events:    make(chan interface{}, 4096),
		namespace: namespace,
		context:   context,
	}
	go s.forward(client)
	return s, nil
}

type Service struct {
	initProcess   *initProcess
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
}

func (s *Service) Create(ctx context.Context, r *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	process, err := newInitProcess(ctx, s.path, s.namespace, r)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.mu.Lock()
	// save the main task id and bundle to the shim for additional requests
	s.id = r.ID
	s.bundle = r.Bundle
	s.initProcess = process
	pid := process.Pid()
	s.processes[r.ID] = process
	s.mu.Unlock()
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.Register(pid, cmd)
	s.events <- &events.TaskCreate{
		ContainerID: r.ID,
		Bundle:      r.Bundle,
		Rootfs:      r.Rootfs,
		IO: &events.TaskIO{
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

func (s *Service) Start(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := s.initProcess.Start(ctx); err != nil {
		return nil, err
	}
	s.events <- &events.TaskStart{
		ContainerID: s.id,
		Pid:         uint32(s.initProcess.Pid()),
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *google_protobuf.Empty) (*shimapi.DeleteResponse, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	p := s.initProcess
	// TODO (@crosbymichael): how to handle errors here
	p.Delete(ctx)
	s.mu.Lock()
	delete(s.processes, p.ID())
	s.mu.Unlock()
	s.events <- &events.TaskDelete{
		ContainerID: s.id,
		ExitStatus:  uint32(p.Status()),
		ExitedAt:    p.ExitedAt(),
		Pid:         uint32(p.Pid()),
	}
	return &shimapi.DeleteResponse{
		ExitStatus: uint32(p.Status()),
		ExitedAt:   p.ExitedAt(),
		Pid:        uint32(p.Pid()),
	}, nil
}

func (s *Service) DeleteProcess(ctx context.Context, r *shimapi.DeleteProcessRequest) (*shimapi.DeleteResponse, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if r.ID == s.initProcess.id {
		return nil, grpc.Errorf(codes.InvalidArgument, "cannot delete init process with DeleteProcess")
	}
	s.mu.Lock()
	p, ok := s.processes[r.ID]
	s.mu.Unlock()
	if !ok {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "process %s not found", r.ID)
	}
	// TODO (@crosbymichael): how to handle errors here
	p.Delete(ctx)
	s.mu.Lock()
	delete(s.processes, p.ID())
	s.mu.Unlock()
	return &shimapi.DeleteResponse{
		ExitStatus: uint32(p.Status()),
		ExitedAt:   p.ExitedAt(),
		Pid:        uint32(p.Pid()),
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *shimapi.ExecProcessRequest) (*shimapi.ExecProcessResponse, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	process, err := newExecProcess(ctx, s.path, r, s.initProcess, r.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	pid := process.Pid()
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.Register(pid, cmd)
	s.processes[r.ID] = process

	s.events <- &events.TaskExecAdded{
		ContainerID: s.id,
		ExecID:      r.ID,
		Pid:         uint32(pid),
	}
	go s.waitExit(process, pid, cmd)
	return &shimapi.ExecProcessResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) ResizePty(ctx context.Context, r *shimapi.ResizePtyRequest) (*google_protobuf.Empty, error) {
	if r.ID == "" {
		return nil, errdefs.ToGRPCf(errdefs.ErrInvalidArgument, "id not provided")
	}
	ws := console.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	s.mu.Lock()
	p, ok := s.processes[r.ID]
	s.mu.Unlock()
	if !ok {
		return nil, errors.Errorf("process does not exist %s", r.ID)
	}
	if err := p.Resize(ws); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

func (s *Service) State(ctx context.Context, r *shimapi.StateRequest) (*shimapi.StateResponse, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	p, ok := s.processes[r.ID]
	if !ok {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process id %s not found", r.ID)
	}
	st, err := s.initProcess.ContainerStatus(ctx)
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
	}
	sio := p.Stdio()
	return &shimapi.StateResponse{
		ID:       p.ID(),
		Bundle:   s.bundle,
		Pid:      uint32(p.Pid()),
		Status:   status,
		Stdin:    sio.stdin,
		Stdout:   sio.stdout,
		Stderr:   sio.stderr,
		Terminal: sio.terminal,
	}, nil
}

func (s *Service) Pause(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := s.initProcess.Pause(ctx); err != nil {
		return nil, err
	}
	s.events <- &events.TaskPaused{
		ContainerID: s.id,
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := s.initProcess.Resume(ctx); err != nil {
		return nil, err
	}
	s.events <- &events.TaskResumed{
		ContainerID: s.id,
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *shimapi.KillRequest) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if r.ID == "" {
		if err := s.initProcess.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, errdefs.ToGRPC(err)
		}
		return empty, nil
	}
	p, ok := s.processes[r.ID]
	if !ok {
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
	p, ok := s.processes[r.ID]
	if !ok {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process does not exist %s", r.ID)
	}
	if err := p.Stdin().Close(); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := s.initProcess.Checkpoint(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.events <- &events.TaskCheckpointed{
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
	if s.initProcess == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	if err := s.initProcess.Update(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

func (s *Service) waitExit(p process, pid int, cmd *reaper.Cmd) {
	status := <-cmd.ExitCh
	p.Exited(status)

	reaper.Default.Delete(pid)
	s.events <- &events.TaskExit{
		ContainerID: s.id,
		ID:          p.ID(),
		Pid:         uint32(pid),
		ExitStatus:  uint32(status),
		ExitedAt:    p.ExitedAt(),
	}
}

func (s *Service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	p, err := s.initProcess.runtime.Ps(ctx, id)
	if err != nil {
		return nil, err
	}
	pids := make([]uint32, 0, len(p))
	for _, pid := range p {
		pids = append(pids, uint32(pid))
	}
	return pids, nil
}

func (s *Service) forward(client poster) {
	for e := range s.events {
		a, err := typeurl.MarshalAny(e)
		if err != nil {
			log.G(s.context).WithError(err).Error("marshal event")
			continue
		}
		if _, err := client.Post(s.context, &events.PostEventRequest{
			Envelope: &events.Envelope{
				Timestamp: time.Now(),
				Topic:     getTopic(e),
				Event:     a,
			},
		}); err != nil {
			log.G(s.context).WithError(err).Error("post event")
		}
	}
}

func getTopic(e interface{}) string {
	switch e.(type) {
	case *events.TaskCreate:
		return runtime.TaskCreateEventTopic
	case *events.TaskStart:
		return runtime.TaskStartEventTopic
	case *events.TaskOOM:
		return runtime.TaskOOMEventTopic
	case *events.TaskExit:
		return runtime.TaskExitEventTopic
	case *events.TaskDelete:
		return runtime.TaskDeleteEventTopic
	case *events.TaskExecAdded:
		return runtime.TaskExecAddedEventTopic
	case *events.TaskPaused:
		return runtime.TaskPausedEventTopic
	case *events.TaskResumed:
		return runtime.TaskResumedEventTopic
	case *events.TaskCheckpointed:
		return runtime.TaskCheckpointedEventTopic
	}
	return "?"
}

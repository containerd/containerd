// +build !windows

package shim

import (
	"fmt"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/console"
	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/types/task"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/reaper"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

const (
	ErrContainerNotCreated = "container hasn't been created yet"
)

var empty = &google_protobuf.Empty{}

const RuncRoot = "/run/containerd/runc"

// NewService returns a new shim service that can be used via GRPC
func NewService(path, namespace string) (*Service, error) {
	if namespace == "" {
		return nil, fmt.Errorf("shim namespace cannot be empty")
	}
	return &Service{
		path:      path,
		processes: make(map[string]process),
		events:    make(chan *events.RuntimeEvent, 4096),
		namespace: namespace,
	}, nil
}

type Service struct {
	initProcess   *initProcess
	path          string
	id            string
	bundle        string
	mu            sync.Mutex
	processes     map[string]process
	events        chan *events.RuntimeEvent
	eventsMu      sync.Mutex
	deferredEvent *events.RuntimeEvent
	namespace     string
}

func (s *Service) Create(ctx context.Context, r *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	if r.ID == "" {
		return nil, grpc.Errorf(codes.InvalidArgument, "task id cannot be empty")
	}
	process, err := newInitProcess(ctx, s.path, s.namespace, r)
	if err != nil {
		return nil, err
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
	s.events <- &events.RuntimeEvent{
		Type:        events.RuntimeEvent_CREATE,
		ID:          r.ID,
		ContainerID: s.id,
		Pid:         uint32(pid),
	}
	go s.waitExit(process, pid, cmd)
	return &shimapi.CreateTaskResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	if err := s.initProcess.Start(ctx); err != nil {
		return nil, err
	}
	s.events <- &events.RuntimeEvent{
		Type:        events.RuntimeEvent_START,
		ID:          s.id,
		ContainerID: s.id,
		Pid:         uint32(s.initProcess.Pid()),
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *google_protobuf.Empty) (*shimapi.DeleteResponse, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	p := s.initProcess
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

func (s *Service) DeleteProcess(ctx context.Context, r *shimapi.DeleteProcessRequest) (*shimapi.DeleteResponse, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	if r.ID == s.initProcess.id {
		return nil, grpc.Errorf(codes.InvalidArgument, "cannot delete init process with DeleteProcess")
	}
	s.mu.Lock()
	p, ok := s.processes[r.ID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process %s not found", r.ID)
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
		return nil, errors.New(ErrContainerNotCreated)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	process, err := newExecProcess(ctx, s.path, r, s.initProcess, r.ID)
	if err != nil {
		return nil, err
	}
	pid := process.Pid()
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.Register(pid, cmd)
	s.processes[r.ID] = process

	s.events <- &events.RuntimeEvent{
		Type:        events.RuntimeEvent_EXEC_ADDED,
		ID:          r.ID,
		ContainerID: s.id,
		Pid:         uint32(pid),
	}
	go s.waitExit(process, pid, cmd)
	return &shimapi.ExecProcessResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) ResizePty(ctx context.Context, r *shimapi.ResizePtyRequest) (*google_protobuf.Empty, error) {
	if r.ID == "" {
		return nil, grpc.Errorf(codes.InvalidArgument, "id not provided")
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
		return nil, err
	}
	return empty, nil
}

func (s *Service) Stream(r *shimapi.StreamEventsRequest, stream shimapi.Shim_StreamServer) error {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()

	if s.deferredEvent != nil {
		if err := stream.Send(s.deferredEvent); err != nil {
			return err
		}
		s.deferredEvent = nil
	}

	for {
		select {
		case e := <-s.events:
			if err := stream.Send(e); err != nil {
				s.deferredEvent = e
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *Service) State(ctx context.Context, r *shimapi.StateRequest) (*shimapi.StateResponse, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	p, ok := s.processes[r.ID]
	if !ok {
		return nil, grpc.Errorf(codes.NotFound, "process id %s not found", r.ID)
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
		return nil, errors.New(ErrContainerNotCreated)
	}
	if err := s.initProcess.Pause(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *google_protobuf.Empty) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	if err := s.initProcess.Resume(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *shimapi.KillRequest) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	if r.ID == "" {
		if err := s.initProcess.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, err
		}
		return empty, nil
	}
	p, ok := s.processes[r.ID]
	if !ok {
		return nil, grpc.Errorf(codes.NotFound, "process id %s not found", r.ID)
	}
	if err := p.Kill(ctx, r.Signal, r.All); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) ListPids(ctx context.Context, r *shimapi.ListPidsRequest) (*shimapi.ListPidsResponse, error) {
	pids, err := s.getContainerPids(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return &shimapi.ListPidsResponse{
		Pids: pids,
	}, nil
}

func (s *Service) CloseIO(ctx context.Context, r *shimapi.CloseIORequest) (*google_protobuf.Empty, error) {
	p, ok := s.processes[r.ID]
	if !ok {
		return nil, grpc.Errorf(codes.NotFound, "process does not exist %s", r.ID)
	}
	if err := p.Stdin().Close(); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) (*google_protobuf.Empty, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
	}
	if err := s.initProcess.Checkpoint(ctx, r); err != nil {
		return nil, err
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
		return nil, errors.New(ErrContainerNotCreated)
	}
	if err := s.initProcess.Update(ctx, r); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) waitExit(p process, pid int, cmd *reaper.Cmd) {
	status := <-cmd.ExitCh
	p.Exited(status)

	reaper.Default.Delete(pid)
	s.events <- &events.RuntimeEvent{
		Type:        events.RuntimeEvent_EXIT,
		ID:          p.ID(),
		ContainerID: s.id,
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

// +build !windows

package shim

import (
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/containerd/console"
	"github.com/containerd/containerd/api/types/task"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/reaper"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
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
		processes: make(map[int]process),
		events:    make(chan *shimapi.Event, 4096),
		namespace: namespace,
	}, nil
}

type Service struct {
	initProcess   *initProcess
	path          string
	id            string
	bundle        string
	mu            sync.Mutex
	processes     map[int]process
	events        chan *shimapi.Event
	eventsMu      sync.Mutex
	deferredEvent *shimapi.Event
	execID        int
	namespace     string
}

func (s *Service) Create(ctx context.Context, r *shimapi.CreateTaskRequest) (*shimapi.CreateTaskResponse, error) {
	process, err := newInitProcess(ctx, s.path, s.namespace, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.id = r.ID
	s.bundle = r.Bundle
	s.initProcess = process
	pid := process.Pid()
	s.processes[pid] = process
	s.mu.Unlock()
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.Register(pid, cmd)
	s.events <- &shimapi.Event{
		Type: shimapi.Event_CREATE,
		ID:   r.ID,
		Pid:  uint32(pid),
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
	s.events <- &shimapi.Event{
		Type: shimapi.Event_START,
		ID:   s.id,
		Pid:  uint32(s.initProcess.Pid()),
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
	delete(s.processes, p.Pid())
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
	if int(r.Pid) == s.initProcess.pid {
		return nil, fmt.Errorf("cannot delete init process with DeleteProcess")
	}
	s.mu.Lock()
	p, ok := s.processes[int(r.Pid)]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process %d not found", r.Pid)
	}
	// TODO (@crosbymichael): how to handle errors here
	p.Delete(ctx)
	s.mu.Lock()
	delete(s.processes, p.Pid())
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
	s.execID++

	process, err := newExecProcess(ctx, s.path, r, s.initProcess, s.execID)
	if err != nil {
		return nil, err
	}
	pid := process.Pid()
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.Register(pid, cmd)
	s.processes[pid] = process

	s.events <- &shimapi.Event{
		Type: shimapi.Event_EXEC_ADDED,
		ID:   s.id,
		Pid:  uint32(pid),
	}
	go s.waitExit(process, pid, cmd)
	return &shimapi.ExecProcessResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) ResizePty(ctx context.Context, r *shimapi.ResizePtyRequest) (*google_protobuf.Empty, error) {
	if r.Pid == 0 {
		return nil, errors.Errorf("pid not provided in request")
	}
	ws := console.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	s.mu.Lock()
	p, ok := s.processes[int(r.Pid)]
	s.mu.Unlock()
	if !ok {
		return nil, errors.Errorf("process does not exist %d", r.Pid)
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

func (s *Service) State(ctx context.Context, r *google_protobuf.Empty) (*shimapi.StateResponse, error) {
	if s.initProcess == nil {
		return nil, errors.New(ErrContainerNotCreated)
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
	o := &shimapi.StateResponse{
		ID:        s.id,
		Bundle:    s.bundle,
		Pid:       uint32(s.initProcess.Pid()),
		Status:    status,
		Processes: []*task.Process{},
		Stdin:     s.initProcess.stdinPath,
		Stdout:    s.initProcess.stdoutPath,
		Stderr:    s.initProcess.stderrPath,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.processes {
		status := task.StatusRunning
		if err := unix.Kill(p.Pid(), 0); err != nil {
			if err != syscall.ESRCH {
				return nil, err
			}
			status = task.StatusStopped
		}
		pp := &task.Process{
			Pid:    uint32(p.Pid()),
			Status: status,
		}
		if ep, ok := p.(*execProcess); ok {
			pp.Stdin = ep.stdinPath
			pp.Stdout = ep.stdoutPath
			pp.Stderr = ep.stderrPath
		}
		o.Processes = append(o.Processes, pp)
	}
	return o, nil
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
	if r.Pid == 0 {
		if err := s.initProcess.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, err
		}
		return empty, nil
	}
	if int(r.Pid) == s.initProcess.pid {
		if err := s.initProcess.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, err
		}
		return empty, nil
	}
	pids, err := s.getContainerPids(ctx, s.initProcess.id)
	if err != nil {
		return nil, err
	}
	valid := false
	for _, p := range pids {
		if r.Pid == p {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errors.Errorf("process %d does not exist in container", r.Pid)
	}
	if err := unix.Kill(int(r.Pid), syscall.Signal(r.Signal)); err != nil {
		return nil, checkKillError(err)
	}
	return empty, nil
}

func (s *Service) ListProcesses(ctx context.Context, r *shimapi.ListProcessesRequest) (*shimapi.ListProcessesResponse, error) {
	pids, err := s.getContainerPids(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	ps := []*task.Process{}
	for _, pid := range pids {
		ps = append(ps, &task.Process{
			Pid: pid,
		})
	}
	resp := &shimapi.ListProcessesResponse{
		Processes: ps,
	}
	return resp, nil
}

func (s *Service) CloseIO(ctx context.Context, r *shimapi.CloseIORequest) (*google_protobuf.Empty, error) {
	p, ok := s.processes[int(r.Pid)]
	if !ok {
		return nil, fmt.Errorf("process does not exist %d", r.Pid)
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

func (s *Service) waitExit(p process, pid int, cmd *reaper.Cmd) {
	status := <-cmd.ExitCh
	p.Exited(status)

	reaper.Default.Delete(pid)
	s.events <- &shimapi.Event{
		Type:       shimapi.Event_EXIT,
		ID:         s.id,
		Pid:        uint32(pid),
		ExitStatus: uint32(status),
		ExitedAt:   p.ExitedAt(),
	}
}

func (s *Service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	p, err := s.initProcess.runc.Ps(ctx, id)
	if err != nil {
		return nil, err
	}

	pids := make([]uint32, 0, len(p))
	for _, pid := range p {
		pids = append(pids, uint32(pid))
	}

	return pids, nil
}

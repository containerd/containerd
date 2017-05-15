// +build !windows

package shim

import (
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/containerd/console"
	shimapi "github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/api/types/container"
	"github.com/containerd/containerd/reaper"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
)

var empty = &google_protobuf.Empty{}

// New returns a new shim service that can be used via GRPC
func New(path string) *Service {
	return &Service{
		path:      path,
		processes: make(map[int]process),
		events:    make(chan *container.Event, 4096),
	}
}

type Service struct {
	initProcess *initProcess
	path        string
	id          string
	bundle      string
	mu          sync.Mutex
	processes   map[int]process
	events      chan *container.Event
	execID      int
}

func (s *Service) Create(ctx context.Context, r *shimapi.CreateRequest) (*shimapi.CreateResponse, error) {
	process, err := newInitProcess(ctx, s.path, r)
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
	go s.waitExit(process, pid, cmd)
	s.events <- &container.Event{
		Type: container.Event_CREATE,
		ID:   r.ID,
		Pid:  uint32(pid),
	}
	return &shimapi.CreateResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *shimapi.StartRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Start(ctx); err != nil {
		return nil, err
	}
	s.events <- &container.Event{
		Type: container.Event_START,
		ID:   s.id,
		Pid:  uint32(s.initProcess.Pid()),
	}
	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *shimapi.DeleteRequest) (*shimapi.DeleteResponse, error) {
	s.mu.Lock()
	p, ok := s.processes[int(r.Pid)]
	s.mu.Unlock()
	if !ok {
		p = s.initProcess
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.processes, p.Pid())
	s.mu.Unlock()
	return &shimapi.DeleteResponse{
		ExitStatus: uint32(p.Status()),
		ExitedAt:   p.ExitedAt(),
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *shimapi.ExecRequest) (*shimapi.ExecResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execID++
	reaper.Default.Lock()
	process, err := newExecProcess(ctx, s.path, r, s.initProcess, s.execID)
	if err != nil {
		reaper.Default.Unlock()
		return nil, err
	}
	pid := process.Pid()
	s.processes[pid] = process
	cmd := &reaper.Cmd{
		ExitCh: make(chan int, 1),
	}
	reaper.Default.RegisterNL(pid, cmd)
	reaper.Default.Unlock()
	go s.waitExit(process, pid, cmd)
	s.events <- &container.Event{
		Type: container.Event_EXEC_ADDED,
		ID:   s.id,
		Pid:  uint32(pid),
	}
	return &shimapi.ExecResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Pty(ctx context.Context, r *shimapi.PtyRequest) (*google_protobuf.Empty, error) {
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

func (s *Service) Events(r *shimapi.EventsRequest, stream shimapi.Shim_EventsServer) error {
	for e := range s.events {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) State(ctx context.Context, r *shimapi.StateRequest) (*shimapi.StateResponse, error) {
	st, err := s.initProcess.ContainerStatus(ctx)
	if err != nil {
		return nil, err
	}
	status := container.Status_UNKNOWN
	switch st {
	case "created":
		status = container.Status_CREATED
	case "running":
		status = container.Status_RUNNING
	case "stopped":
		status = container.Status_STOPPED
	case "paused":
		status = container.Status_PAUSED
	}
	o := &shimapi.StateResponse{
		ID:        s.id,
		Bundle:    s.bundle,
		Pid:       uint32(s.initProcess.Pid()),
		Status:    status,
		Processes: []*container.Process{},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.processes {
		status := container.Status_RUNNING
		if err := unix.Kill(p.Pid(), 0); err != nil {
			if err != syscall.ESRCH {
				return nil, err
			}
			status = container.Status_STOPPED
		}
		o.Processes = append(o.Processes, &container.Process{
			Pid:    uint32(p.Pid()),
			Status: status,
		})
	}
	return o, nil
}

func (s *Service) Pause(ctx context.Context, r *shimapi.PauseRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Pause(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *shimapi.ResumeRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Resume(ctx); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Exit(ctx context.Context, r *shimapi.ExitRequest) (*google_protobuf.Empty, error) {
	// signal ourself to exit
	if err := unix.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *shimapi.KillRequest) (*google_protobuf.Empty, error) {
	if r.Pid == 0 {
		if err := s.initProcess.Kill(ctx, r.Signal, r.All); err != nil {
			return nil, err
		}
		return empty, nil
	}
	proc, ok := s.processes[int(r.Pid)]
	if !ok {
		return nil, fmt.Errorf("process does not exist %d", r.Pid)
	}
	if err := proc.Signal(int(r.Signal)); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) CloseStdin(ctx context.Context, r *shimapi.CloseStdinRequest) (*google_protobuf.Empty, error) {
	p, ok := s.processes[int(r.Pid)]
	if !ok {
		return nil, fmt.Errorf("process does not exist %d", r.Pid)
	}
	if err := p.Stdin().Close(); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) waitExit(p process, pid int, cmd *reaper.Cmd) {
	status := <-cmd.ExitCh
	p.Exited(status)
	s.events <- &container.Event{
		Type:       container.Event_EXIT,
		ID:         s.id,
		Pid:        uint32(pid),
		ExitStatus: uint32(status),
		ExitedAt:   p.ExitedAt(),
	}
}

package shim

import (
	"sync"
	"syscall"

	"github.com/crosbymichael/console"
	apishim "github.com/docker/containerd/api/shim"
	"github.com/docker/containerd/utils"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var emptyResponse = &google_protobuf.Empty{}

// New returns a new shim service that can be used via GRPC
func New() *Service {
	return &Service{
		processes: make(map[int]process),
		events:    make(chan *apishim.Event, 4096),
	}
}

type Service struct {
	initProcess *initProcess
	id          string
	bundle      string
	mu          sync.Mutex
	processes   map[int]process
	events      chan *apishim.Event
	execID      int
}

func (s *Service) Create(ctx context.Context, r *apishim.CreateRequest) (*apishim.CreateResponse, error) {
	process, err := newInitProcess(ctx, r)
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
	s.events <- &apishim.Event{
		Type: apishim.EventType_CREATE,
		ID:   r.ID,
		Pid:  uint32(pid),
	}
	return &apishim.CreateResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *apishim.StartRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Start(ctx); err != nil {
		return nil, err
	}
	s.events <- &apishim.Event{
		Type: apishim.EventType_START,
		ID:   s.id,
		Pid:  uint32(s.initProcess.Pid()),
	}
	return emptyResponse, nil
}

func (s *Service) Delete(ctx context.Context, r *apishim.DeleteRequest) (*apishim.DeleteResponse, error) {
	s.mu.Lock()
	p, ok := s.processes[int(r.Pid)]
	s.mu.Unlock()
	if !ok {
		return nil, errors.Errorf("process does not exist %d", r.Pid)
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.processes, p.Pid())
	s.mu.Unlock()
	return &apishim.DeleteResponse{
		ExitStatus: uint32(p.Status()),
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *apishim.ExecRequest) (*apishim.ExecResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execID++
	process, err := newExecProcess(ctx, r, s.initProcess, s.execID)
	if err != nil {
		return nil, err
	}
	pid := process.Pid()
	s.processes[pid] = process
	s.events <- &apishim.Event{
		Type: apishim.EventType_EXEC_ADDED,
		ID:   s.id,
		Pid:  uint32(pid),
	}
	return &apishim.ExecResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Pty(ctx context.Context, r *apishim.PtyRequest) (*google_protobuf.Empty, error) {
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
	return emptyResponse, nil
}

func (s *Service) Events(r *apishim.EventsRequest, stream apishim.Shim_EventsServer) error {
	for e := range s.events {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) State(ctx context.Context, r *apishim.StateRequest) (*apishim.StateResponse, error) {
	o := &apishim.StateResponse{
		ID:        s.id,
		Bundle:    s.bundle,
		InitPid:   uint32(s.initProcess.Pid()),
		Processes: []*apishim.Process{},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.processes {
		state := apishim.State_RUNNING
		if err := syscall.Kill(p.Pid(), 0); err != nil {
			if err != syscall.ESRCH {
				return nil, err
			}
			state = apishim.State_STOPPED
		}
		o.Processes = append(o.Processes, &apishim.Process{
			Pid:   uint32(p.Pid()),
			State: state,
		})
	}
	return o, nil
}

func (s *Service) Pause(ctx context.Context, r *apishim.PauseRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Pause(ctx); err != nil {
		return nil, err
	}
	return emptyResponse, nil
}

func (s *Service) Resume(ctx context.Context, r *apishim.ResumeRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Resume(ctx); err != nil {
		return nil, err
	}
	return emptyResponse, nil
}

func (s *Service) ProcessExit(e utils.Exit) error {
	s.mu.Lock()
	if p, ok := s.processes[e.Pid]; ok {
		p.Exited(e.Status)
		s.events <- &apishim.Event{
			Type:       apishim.EventType_EXIT,
			ID:         s.id,
			Pid:        uint32(p.Pid()),
			ExitStatus: uint32(e.Status),
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) GetLog(ctx context.Context, r *apishim.GetLogRequest) (*apishim.GetLogResponse, error) {
	log, _, err := readInitProcessLog()
	if err != nil {
		return nil, err
	}
	o := &apishim.GetLogResponse{
		Log: log,
	}
	return o, nil
}

package shim

import (
	"fmt"
	"sync"

	runc "github.com/crosbymichael/go-runc"
	apishim "github.com/docker/containerd/api/shim"
	"github.com/docker/containerd/utils"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
)

var emptyResponse = &google_protobuf.Empty{}

// NewService returns a new shim service that can be used via GRPC
func NewService() *Service {
	return &Service{
		processes: make(map[int]process),
		events:    make(chan *apishim.Event, 2048),
	}
}

type Service struct {
	initProcess *initProcess
	id          string
	mu          sync.Mutex
	processes   map[int]process
	events      chan *apishim.Event
}

func (s *Service) Create(ctx context.Context, r *apishim.CreateRequest) (*apishim.CreateResponse, error) {
	process, err := newInitProcess(ctx, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.initProcess = process
	pid := process.Pid()
	s.processes[pid] = process
	s.id = r.ID
	s.mu.Unlock()
	return &apishim.CreateResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Start(ctx context.Context, r *apishim.StartRequest) (*google_protobuf.Empty, error) {
	if err := s.initProcess.Start(ctx); err != nil {
		return nil, err
	}
	return emptyResponse, nil
}

func (s *Service) Delete(ctx context.Context, r *apishim.DeleteRequest) (*apishim.DeleteResponse, error) {
	if err := s.initProcess.Delete(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.processes, s.initProcess.pid)
	s.mu.Unlock()
	return &apishim.DeleteResponse{
		ExitStatus: uint32(s.initProcess.Status()),
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *apishim.ExecRequest) (*apishim.ExecResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	process, err := newExecProcess(ctx, r, s.initProcess)
	if err != nil {
		return nil, err
	}
	pid := process.Pid()
	s.processes[pid] = process
	return &apishim.ExecResponse{
		Pid: uint32(pid),
	}, nil
}

func (s *Service) Pty(ctx context.Context, r *apishim.PtyRequest) (*google_protobuf.Empty, error) {
	ws := runc.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	s.mu.Lock()
	p, ok := s.processes[int(r.Pid)]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process does not exist %d", r.Pid)
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

package main

import (
	"github.com/docker/containerd/api/shim"
	"github.com/docker/containerd/utils"
	"github.com/docker/docker/pkg/term"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
)

type service struct {
	init *process
}

func (s *service) Create(ctx context.Context, r *shim.CreateRequest) (*shim.CreateResponse, error) {
	process, err := newProcess(r.ID, r.Bundle, r.Runtime)
	if err != nil {
		return nil, err
	}
	s.init = process
	if err := process.create(); err != nil {
		return nil, err
	}
	return &shim.CreateResponse{
		Pid: uint32(process.pid()),
	}, nil
}

func (s *service) Start(ctx context.Context, r *shim.StartRequest) (*google_protobuf.Empty, error) {
	err := s.init.start()
	return nil, err
}

func (s *service) Delete(ctx context.Context, r *shim.DeleteRequest) (*shim.DeleteResponse, error) {
	// TODO: error when container has not stopped
	err := s.init.killAll()
	s.init.Wait()
	if derr := s.init.delete(); err == nil {
		err = derr
	}
	if cerr := s.init.Close(); err == nil {
		err = cerr
	}
	return &shim.DeleteResponse{
		ExitStatus: uint32(s.init.exitStatus),
	}, err
}

func (s *service) Exec(ctx context.Context, r *shim.ExecRequest) (*shim.ExecResponse, error) {
	return nil, nil
}

func (s *service) Pty(ctx context.Context, r *shim.PtyRequest) (*google_protobuf.Empty, error) {
	if s.init.console == nil {
		return nil, nil
	}
	ws := term.Winsize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	if err := term.SetWinsize(s.init.console.Fd(), &ws); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *service) processExited(e utils.Exit) error {
	if s.init.pid() == e.Pid {
		s.init.setExited(e.Status)
	}
	return nil
}

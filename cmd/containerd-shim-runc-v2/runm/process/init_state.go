package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

type initState interface {
	Start(context.Context) error
	Delete(context.Context) error
	Kill(context.Context, uint32, bool) error
	SetExited(int)
	Status(context.Context) (string, error)
}

type createdState struct {
	p *Init

	status string
}

func (s *createdState) Start(ctx context.Context) error {
	s.status = "running"

	if _, err := os.Stat(filepath.Join(s.p.Rootfs, "model")); err == nil {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		rootPath := filepath.Dir(exePath)
		args := []string{
			"serve",
			"--port", "11111",
			"--rootfs", s.p.Rootfs,
		}
		cmd := exec.Command(filepath.Join(rootPath, "runm"), args...)
		err = cmd.Start()
		if err == nil {
			s.p.pid = cmd.Process.Pid
		}
		return err
	}
	return nil
}

func (s *createdState) Delete(ctx context.Context) error {
	s.status = "deleted"
	return nil
}

func (s *createdState) Kill(ctx context.Context, sig uint32, all bool) error {
	if s.status == "stopped" {
		return nil
	}
	s.status = "stopped"
	return s.p.kill(ctx, sig, all)
}

func (s *createdState) SetExited(status int) {
	s.p.setExited(status)
	s.status = "stopped"
}

func (s *createdState) Status(ctx context.Context) (string, error) {
	return s.status, nil
}

// +build !windows

package shim

import (
	"context"

	"github.com/containerd/console"
	"github.com/containerd/containerd/errdefs"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/pkg/errors"
)

type initState interface {
	processState

	Pause(context.Context) error
	Resume(context.Context) error
	Update(context.Context, *shimapi.UpdateTaskRequest) error
	Checkpoint(context.Context, *shimapi.CheckpointTaskRequest) error
}

type createdState struct {
	p *initProcess
}

func (s *createdState) transition(name string) error {
	switch name {
	case "running":
		s.p.initState = &runningState{p: s.p}
	case "stopped":
		s.p.initState = &stoppedState{p: s.p}
	case "deleted":
		s.p.initState = &deletedState{}
	default:
		return errors.Errorf("invalid state transition %q to %q", stateName(s), name)
	}
	return nil
}

func (s *createdState) Pause(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot pause task in created state")
}

func (s *createdState) Resume(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot resume task in created state")
}

func (s *createdState) Update(context context.Context, r *shimapi.UpdateTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.update(context, r)
}

func (s *createdState) Checkpoint(context context.Context, r *shimapi.CheckpointTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot checkpoint a task in created state")
}

func (s *createdState) Resize(ws console.WinSize) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.resize(ws)
}

func (s *createdState) Start(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	if err := s.p.start(ctx); err != nil {
		return err
	}
	return s.transition("running")
}

func (s *createdState) Delete(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	if err := s.p.delete(ctx); err != nil {
		return err
	}
	return s.transition("deleted")
}

func (s *createdState) Kill(ctx context.Context, sig uint32, all bool) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.kill(ctx, sig, all)
}

func (s *createdState) SetExited(status int) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.p.setExited(status)

	if err := s.transition("stopped"); err != nil {
		panic(err)
	}
}

type runningState struct {
	p *initProcess
}

func (s *runningState) transition(name string) error {
	switch name {
	case "stopped":
		s.p.initState = &stoppedState{p: s.p}
	case "paused":
		s.p.initState = &pausedState{p: s.p}
	default:
		return errors.Errorf("invalid state transition %q to %q", stateName(s), name)
	}
	return nil
}

func (s *runningState) Pause(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	if err := s.p.pause(ctx); err != nil {
		return err
	}
	return s.transition("paused")
}

func (s *runningState) Resume(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot resume a running process")
}

func (s *runningState) Update(context context.Context, r *shimapi.UpdateTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.update(context, r)
}

func (s *runningState) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.checkpoint(ctx, r)
}

func (s *runningState) Resize(ws console.WinSize) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.resize(ws)
}

func (s *runningState) Start(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot start a running process")
}

func (s *runningState) Delete(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot delete a running process")
}

func (s *runningState) Kill(ctx context.Context, sig uint32, all bool) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.kill(ctx, sig, all)
}

func (s *runningState) SetExited(status int) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.p.setExited(status)

	if err := s.transition("stopped"); err != nil {
		panic(err)
	}
}

type pausedState struct {
	p *initProcess
}

func (s *pausedState) transition(name string) error {
	switch name {
	case "running":
		s.p.initState = &runningState{p: s.p}
	case "stopped":
		s.p.initState = &stoppedState{p: s.p}
	default:
		return errors.Errorf("invalid state transition %q to %q", stateName(s), name)
	}
	return nil
}

func (s *pausedState) Pause(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot pause a paused container")
}

func (s *pausedState) Resume(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	if err := s.p.resume(ctx); err != nil {
		return err
	}
	return s.transition("running")
}

func (s *pausedState) Update(context context.Context, r *shimapi.UpdateTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.update(context, r)
}

func (s *pausedState) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.checkpoint(ctx, r)
}

func (s *pausedState) Resize(ws console.WinSize) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.resize(ws)
}

func (s *pausedState) Start(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot start a paused process")
}

func (s *pausedState) Delete(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot delete a paused process")
}

func (s *pausedState) Kill(ctx context.Context, sig uint32, all bool) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.p.kill(ctx, sig, all)
}

func (s *pausedState) SetExited(status int) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.p.setExited(status)

	if err := s.transition("stopped"); err != nil {
		panic(err)
	}
}

type stoppedState struct {
	p *initProcess
}

func (s *stoppedState) transition(name string) error {
	switch name {
	case "deleted":
		s.p.initState = &deletedState{}
	default:
		return errors.Errorf("invalid state transition %q to %q", stateName(s), name)
	}
	return nil
}

func (s *stoppedState) Pause(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot pause a stopped container")
}

func (s *stoppedState) Resume(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot resume a stopped container")
}

func (s *stoppedState) Update(context context.Context, r *shimapi.UpdateTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot update a stopped container")
}

func (s *stoppedState) Checkpoint(ctx context.Context, r *shimapi.CheckpointTaskRequest) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot checkpoint a stopped container")
}

func (s *stoppedState) Resize(ws console.WinSize) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot resize a stopped container")
}

func (s *stoppedState) Start(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return errors.Errorf("cannot start a stopped process")
}

func (s *stoppedState) Delete(ctx context.Context) error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	if err := s.p.delete(ctx); err != nil {
		return err
	}
	return s.transition("deleted")
}

func (s *stoppedState) Kill(ctx context.Context, sig uint32, all bool) error {
	return errdefs.ToGRPCf(errdefs.ErrNotFound, "process %s not found", s.p.id)
}

func (s *stoppedState) SetExited(status int) {
	// no op
}

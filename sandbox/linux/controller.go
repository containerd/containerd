//go:build linux
// +build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package linux

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	osinterface "github.com/containerd/containerd/pkg/os"
	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/pkg/stdio"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/runtime/v2/runc"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/sys/reaper"
	runcC "github.com/containerd/go-runc"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type sandboxController struct {
	root          string
	state         string
	pauseLocation string
	taskAddress   string
	os            osinterface.OS
	platform      stdio.Platform
	sandboxes     map[string]runcSandbox
}

func NewSandboxController(ctx context.Context, root string, state string, pauseLocation string, taskAddress string) (sandbox.Controller, error) {
	platform, err := runc.NewPlatform()
	if err != nil {
		return nil, err
	}
	signals := make(chan os.Signal, 32)
	smp := []os.Signal{unix.SIGCHLD}
	signal.Notify(signals, smp...)
	go reap(ctx, signals)
	if err := reaper.SetSubreaper(1); err != nil {
		return nil, err
	}
	ec := reaper.Default.Subscribe()

	controller := &sandboxController{
		root:          root,
		state:         state,
		pauseLocation: pauseLocation,
		taskAddress:   taskAddress,
		platform:      platform,
		sandboxes:     make(map[string]runcSandbox),
		os:            osinterface.RealOS{},
	}
	if err := controller.recover(); err != nil {
		return nil, err
	}
	go controller.processExits(ec)

	return controller, nil
}

func (s *sandboxController) Start(ctx context.Context, sandbox *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	log.G(ctx).Debugf("start runc sandbox %s with a pause container", sandbox.ID)
	sandbox.Spec.Process.Args = []string{"/pause"}
	specAny, err := typeurl.MarshalAny(sandbox.Spec)
	if err != nil {
		return nil, err
	}
	bundle, err := v2.NewBundle(ctx, s.root, s.state, sandbox.ID, specAny)
	if err != nil {
		return nil, err
	}
	rootfs := filepath.Join(bundle.Path, "rootfs")
	if err := os.Mkdir(rootfs, 0711); err != nil && !os.IsExist(err) {
		return nil, err
	}
	err = s.os.CopyFile(s.pauseLocation, filepath.Join(rootfs, "pause"), 0755)
	if err != nil {
		return nil, err
	}
	pauseContainerRequest := &task.CreateTaskRequest{
		ID:     sandbox.ID,
		Bundle: bundle.Path,
	}
	c, err := runc.NewContainer(ctx, s.platform, pauseContainerRequest)
	if err != nil {
		return nil, err
	}
	_, err = c.Start(ctx, &task.StartRequest{
		ID: sandbox.ID,
	})
	if err != nil {
		return nil, err
	}
	sb := runcSandbox{
		bundle:         bundle,
		pauseContainer: c,
		containers:     nil,
	}
	s.sandboxes[sandbox.ID] = sb
	sandbox.TaskAddress = s.taskAddress
	log.G(ctx).Debugf("succeed start runc sandbox %s", sandbox.ID)
	return sandbox, nil
}

func (s *sandboxController) Shutdown(ctx context.Context, id string) error {
	log.G(ctx).Debugf("shutdown runc sandbox %s with a pause container", id)
	sb, ok := s.sandboxes[id]
	if !ok {
		return fmt.Errorf("sandbox %s is not found: %w", id, errdefs.ErrNotFound)
	}
	c := sb.pauseContainer
	err := c.Kill(ctx, &task.KillRequest{
		ID:     id,
		ExecID: "",
		Signal: 9,
		All:    true,
	})
	if err != nil {
		return err
	}
	if err := sb.Wait(); err != nil {
		return err
	}
	_, err = c.Delete(ctx, &task.DeleteRequest{
		ID:     id,
		ExecID: "",
	})
	if err != nil {
		return err
	}

	rootfs := filepath.Join(sb.bundle.Path, "rootfs")
	if err := os.RemoveAll(rootfs); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := sb.bundle.Delete(); err != nil {
		return err
	}
	delete(s.sandboxes, id)
	log.G(ctx).Debugf("succeed shutdown runc sandbox %s", id)
	return nil
}

func (s *sandboxController) Pause(ctx context.Context, id string) error {
	sb, ok := s.sandboxes[id]
	if !ok {
		return fmt.Errorf("sandbox %s is not found: %w", id, errdefs.ErrNotFound)
	}
	return sb.Pause(ctx)
}

func (s *sandboxController) Resume(ctx context.Context, id string) error {
	sb, ok := s.sandboxes[id]
	if !ok {
		return fmt.Errorf("sandbox %s is not found: %w", id, errdefs.ErrNotFound)
	}
	return sb.Resume(ctx)
}

func (s *sandboxController) Update(ctx context.Context, sandboxID string, sandbox *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	//TODO implement me
	panic("implement me")
}

func (s *sandboxController) AppendContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	log.G(ctx).Debugf("append container %s to sandbox %s", container.ID, sandboxID)
	_, ok := s.sandboxes[sandboxID]
	if !ok {
		return nil, fmt.Errorf("sandbox %s is not found: %w", sandboxID, errdefs.ErrNotFound)
	}

	log.G(ctx).Debugf("succeed append container %s to sandbox %s", container.ID, sandboxID)
	return container, nil
}

func (s *sandboxController) UpdateContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	return container, nil
}

func (s *sandboxController) RemoveContainer(ctx context.Context, sandboxID string, id string) error {
	return nil
}

func (s *sandboxController) Status(ctx context.Context, id string) (sandbox.Status, error) {
	sb, ok := s.sandboxes[id]
	if !ok {
		return sandbox.Status{}, fmt.Errorf("sandbox %s is not found: %w", id, errdefs.ErrNotFound)
	}
	pid := sb.pauseContainer.Pid()
	return sandbox.Status{ID: id, State: sandbox.StateReady, PID: uint32(pid)}, nil
}

func (s *sandboxController) Ping(ctx context.Context, id string) error {
	_, ok := s.sandboxes[id]
	if !ok {
		return fmt.Errorf("sandbox %s is not found: %w", id, errdefs.ErrNotFound)
	}
	return nil
}

func (s *sandboxController) processExits(ec chan runcC.Exit) {
	for e := range ec {
		s.checkProcesses(e)
	}
}

func (s *sandboxController) checkProcesses(e runcC.Exit) {
	for id, sb := range s.sandboxes {
		if e.Pid != sb.pauseContainer.Pid() {
			continue
		}
		p, err := sb.pauseContainer.Process("")
		if err != nil {
			logrus.WithError(err).Warnf("failed to get process of sandbox %s", id)
			continue
		}
		p.SetExited(e.Status)
		return
	}
}

func (s *sandboxController) recover() error {
	for _, ns := range []string{"k8s.io", "moby", "default"} {
		// TODO add runc configs for sandboxer
		runtime := process.NewRunc("", s.state, ns, "", false)
		containers, err := runtime.List(context.Background())
		if err != nil {
			return err
		}
		existingSandboxes := make(map[string]*runcSandbox)
		for _, c := range containers {
			if !strings.HasPrefix(c.Bundle, s.state) {
				continue
			}
			cont, err := runc.RecoverContainer(runtime, c, ns, s.platform)
			if err != nil {
				return err
			}
			sb := runcSandbox{
				bundle: &v2.Bundle{
					ID:        c.ID,
					Path:      c.Bundle,
					Namespace: ns,
				},
				pauseContainer: cont,
				containers:     nil,
			}
			s.sandboxes[c.ID] = sb
			existingSandboxes[c.ID] = &sb
		}
		go func() {
			for {
				if len(existingSandboxes) == 0 {
					return
				}
				currentContainers, err := runtime.List(context.Background())
				if err != nil {
					logrus.Warningf("failed to list containers when monitoring existing sandboxes")
					return
				}
				stillRunningSandboxes := make(map[string]*runcSandbox)
				for id, sb := range existingSandboxes {
					running := false
					for _, newC := range currentContainers {
						if id == newC.ID {
							p, err := sb.pauseContainer.Process("")
							if err != nil {
								logrus.Warningf("failed to get process of container %s", id)
								break
							}
							if newC.Status != "running" {
								// can not get the exit code
								p.SetExited(-1)
								break
							}
							running = true
						}
					}
					if running {
						stillRunningSandboxes[id] = sb
					}
				}
				existingSandboxes = stillRunningSandboxes
				time.Sleep(1 * time.Second)
			}
		}()
	}
	return nil
}

func reap(ctx context.Context, signals chan os.Signal) {
	log.G(ctx).Info("starting signal loop")

	for {
		select {
		case <-ctx.Done():
			return
		case s := <-signals:
			// Exit signals are handled separately from this loop
			// They get registered with this channel so that we can ignore such signals for short-running actions (e.g. `delete`)
			switch s {
			case unix.SIGCHLD:
				if err := reaper.Reap(); err != nil {
					log.G(ctx).WithError(err).Error("reap exit status")
				}
			case unix.SIGPIPE:
			}
		}
	}
}

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

package embed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/containerd/cgroups"
	cgroupsv2 "github.com/containerd/cgroups/v2"
	eventstypes "github.com/containerd/containerd/api/events"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/oom"
	oomv1 "github.com/containerd/containerd/pkg/oom/v1"
	oomv2 "github.com/containerd/containerd/pkg/oom/v2"
	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/pkg/stdio"
	"github.com/containerd/containerd/pkg/userns"
	"github.com/containerd/containerd/protobuf"
	ptypes "github.com/containerd/containerd/protobuf/types"
	"github.com/containerd/containerd/runtime/v2/runc"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/containerd/sys"
	"github.com/containerd/containerd/sys/reaper"
	runcC "github.com/containerd/go-runc"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"
)

var (
	_     = (taskAPI.TaskService)(&service{})
	empty = &ptypes.Empty{}
)

type TaskConfig struct {
	Root         string `toml:"root" json:"root"`
	Binary       string `toml:"binary" json:"binary"`
	TTRPCAddress string `toml:"ttrpc_address" json:"ttrpc_address"`
}

// NewTaskService creates a new instance of a task service
func NewTaskService(ctx context.Context, publisher shim.Publisher, path string, config *TaskConfig) (taskAPI.TaskService, error) {
	var (
		ep  oom.Watcher
		err error
	)
	if cgroups.Mode() == cgroups.Unified {
		ep, err = oomv2.New(publisher)
	} else {
		ep, err = oomv1.New(publisher)
	}
	if err != nil {
		return nil, err
	}
	go ep.Run(ctx)
	s := &service{
		context:         ctx,
		events:          make(chan interface{}, 128),
		ec:              reaper.Default.Subscribe(),
		ep:              ep,
		containers:      make(map[string]*runc.Container),
		recoverFinished: make(chan struct{}),
	}
	if err := s.initPlatform(); err != nil {
		return nil, fmt.Errorf("failed to initialized platform behavior: %w", err)
	}
	if err := s.recover(config.Binary, config.Root, path); err != nil {
		return nil, err
	}
	go s.processExits()
	runcC.Monitor = reaper.Default
	go s.forward(ctx, publisher)

	l, err := sys.GetLocalListener(strings.TrimPrefix(config.TTRPCAddress, "unix://"), 0, 0)
	if err != nil {
		return nil, err
	}
	ttrpcServer, err := ttrpc.NewServer()
	if err != nil {
		return nil, err
	}
	taskAPI.RegisterTaskService(ttrpcServer, s)
	go func() {
		if err := ttrpcServer.Serve(ctx, l); err != nil {
			logrus.Errorf("failed to server runc task server with address %s", config.TTRPCAddress)
		}
	}()
	return s, nil
}

// service is the shim implementation of a remote shim over GRPC
type service struct {
	mu          sync.Mutex
	eventSendMu sync.Mutex

	context  context.Context
	events   chan interface{}
	platform stdio.Platform
	ec       chan runcC.Exit
	ep       oom.Watcher

	containers      map[string]*runc.Container
	recoverFinished chan struct{}
}

// Create a new initial process and container with the underlying OCI runtime
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (_ *taskAPI.CreateTaskResponse, err error) {
	log.G(ctx).Debugf("create runc container %s", r.ID)
	defer func() {
		if err != nil {
			log.G(ctx).Debugf("failed to create runc container %s: %v", r.ID, err)
		} else {
			log.G(ctx).Debugf("succeed to create runc container %s", r.ID)
		}

	}()
	container, err := runc.NewContainer(ctx, s.platform, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[r.ID] = container
	s.send(&eventstypes.TaskCreate{
		ContainerID: r.ID,
		Bundle:      r.Bundle,
		Rootfs:      r.Rootfs,
		IO: &eventstypes.TaskIO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		Checkpoint: r.Checkpoint,
		Pid:        uint32(container.Pid()),
	})

	return &taskAPI.CreateTaskResponse{
		Pid: uint32(container.Pid()),
	}, nil
}

func (s *service) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTaskService(server, s)
	return nil
}

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (_ *taskAPI.StartResponse, err error) {
	log.G(ctx).Debugf("start runc container %s", r.ID)
	defer func() {
		if err != nil {
			log.G(ctx).Debugf("failed to start runc container %s: %v", r.ID, err)
		} else {
			log.G(ctx).Debugf("succeed to start runc container %s", r.ID)
		}
	}()
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}

	// hold the send lock so that the start events are sent before any exit events in the error case
	s.eventSendMu.Lock()
	p, err := container.Start(ctx, r)
	if err != nil {
		s.eventSendMu.Unlock()
		return nil, errdefs.ToGRPC(err)
	}

	switch r.ExecID {
	case "":
		switch cg := container.Cgroup().(type) {
		case cgroups.Cgroup:
			if err := s.ep.Add(container.ID, cg); err != nil {
				logrus.WithError(err).Error("add cg to OOM monitor")
			}
		case *cgroupsv2.Manager:
			allControllers, err := cg.RootControllers()
			if err != nil {
				logrus.WithError(err).Error("failed to get root controllers")
			} else {
				if err := cg.ToggleControllers(allControllers, cgroupsv2.Enable); err != nil {
					if userns.RunningInUserNS() {
						logrus.WithError(err).Debugf("failed to enable controllers (%v)", allControllers)
					} else {
						logrus.WithError(err).Errorf("failed to enable controllers (%v)", allControllers)
					}
				}
			}
			if err := s.ep.Add(container.ID, cg); err != nil {
				logrus.WithError(err).Error("add cg to OOM monitor")
			}
		}

		s.send(&eventstypes.TaskStart{
			ContainerID: container.ID,
			Pid:         uint32(p.Pid()),
		})
	default:
		s.send(&eventstypes.TaskExecStarted{
			ContainerID: container.ID,
			ExecID:      r.ExecID,
			Pid:         uint32(p.Pid()),
		})
	}
	s.eventSendMu.Unlock()
	return &taskAPI.StartResponse{
		Pid: uint32(p.Pid()),
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (_ *taskAPI.DeleteResponse, err error) {
	log.G(ctx).Debugf("delete runc container %s", r.ID)
	defer func() {
		if err != nil {
			log.G(ctx).Debugf("failed to delete runc container %s: %v", r.ID, err)
		} else {
			log.G(ctx).Debugf("succeed to delete runc container %s", r.ID)
		}

	}()
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Delete(ctx, r)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	// if we deleted an init task, send the task delete event
	if r.ExecID == "" {
		s.mu.Lock()
		delete(s.containers, r.ID)
		s.mu.Unlock()
		s.send(&eventstypes.TaskDelete{
			ContainerID: container.ID,
			Pid:         uint32(p.Pid()),
			ExitStatus:  uint32(p.ExitStatus()),
			ExitedAt:    protobuf.ToTimestamp(p.ExitedAt()),
		})
	}
	return &taskAPI.DeleteResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   protobuf.ToTimestamp(p.ExitedAt()),
		Pid:        uint32(p.Pid()),
	}, nil
}

// Exec an additional process inside the container
func (s *service) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	ok, cancel := container.ReserveProcess(r.ExecID)
	if !ok {
		return nil, errdefs.ToGRPCf(errdefs.ErrAlreadyExists, "id %s", r.ExecID)
	}
	process, err := container.Exec(ctx, r)
	if err != nil {
		cancel()
		return nil, errdefs.ToGRPC(err)
	}

	s.send(&eventstypes.TaskExecAdded{
		ContainerID: container.ID,
		ExecID:      process.ID(),
	})
	return empty, nil
}

// ResizePty of a process
func (s *service) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.ResizePty(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

// State returns runtime state information for a process
func (s *service) State(ctx context.Context, r *taskAPI.StateRequest) (ret *taskAPI.StateResponse, retErr error) {
	log.G(ctx).Debugf("state runc container %s", r.ID)
	defer func() {
		if retErr != nil {
			log.G(ctx).Debugf("failed to state runc container %s: %v", r.ID, retErr)
		} else {
			log.G(ctx).Debugf("succeed to state runc container %s, status %v", r.ID, ret)
		}
	}()
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Process(r.ExecID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	st, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	status := task.Status_UNKNOWN
	switch st {
	case "created":
		status = task.Status_CREATED
	case "running":
		status = task.Status_RUNNING
	case "stopped":
		status = task.Status_STOPPED
	case "paused":
		status = task.Status_PAUSED
	case "pausing":
		status = task.Status_PAUSING
	}
	sio := p.Stdio()
	return &taskAPI.StateResponse{
		ID:         p.ID(),
		Bundle:     container.Bundle,
		Pid:        uint32(p.Pid()),
		Status:     status,
		Stdin:      sio.Stdin,
		Stdout:     sio.Stdout,
		Stderr:     sio.Stderr,
		Terminal:   sio.Terminal,
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   protobuf.ToTimestamp(p.ExitedAt()),
	}, nil
}

// Pause the container
func (s *service) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.Pause(ctx); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.send(&eventstypes.TaskPaused{
		ContainerID: container.ID,
	})
	return empty, nil
}

// Resume the container
func (s *service) Resume(ctx context.Context, r *taskAPI.ResumeRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.Resume(ctx); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	s.send(&eventstypes.TaskResumed{
		ContainerID: container.ID,
	})
	return empty, nil
}

// Kill a process with the provided signal
func (s *service) Kill(ctx context.Context, r *taskAPI.KillRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.Kill(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

// Pids returns all pids inside the container
func (s *service) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	pids, err := s.getContainerPids(ctx, r.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var processes []*task.ProcessInfo
	for _, pid := range pids {
		pInfo := task.ProcessInfo{
			Pid: pid,
		}
		for _, p := range container.ExecdProcesses() {
			if p.Pid() == int(pid) {
				d := &options.ProcessDetails{
					ExecID: p.ID(),
				}
				a, err := protobuf.MarshalAnyToProto(d)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal process %d info: %w", pid, err)
				}
				pInfo.Info = a
				break
			}
		}
		processes = append(processes, &pInfo)
	}
	return &taskAPI.PidsResponse{
		Processes: processes,
	}, nil
}

// CloseIO of a process
func (s *service) CloseIO(ctx context.Context, r *taskAPI.CloseIORequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.CloseIO(ctx, r); err != nil {
		return nil, err
	}
	return empty, nil
}

// Checkpoint the container
func (s *service) Checkpoint(ctx context.Context, r *taskAPI.CheckpointTaskRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.Checkpoint(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

// Update a running container
func (s *service) Update(ctx context.Context, r *taskAPI.UpdateTaskRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	if err := container.Update(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

// Wait for a process to exit
func (s *service) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	logrus.Debugf("wating for process %s of container %s", r.ExecID, r.ID)
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Process(r.ExecID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	p.Wait()
	logrus.Debugf("succeed wating for process %s of container %s, status %v", r.ExecID, r.ID, p.ExitStatus())
	return &taskAPI.WaitResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   protobuf.ToTimestamp(p.ExitedAt()),
	}, nil
}

// Connect returns shim information such as the shim's pid
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	var pid int
	log.G(ctx).Debugf("connect to container %s", r.ID)
	<-s.recoverFinished
	if container, err := s.getContainer(r.ID); err == nil {
		pid = container.Pid()
	}
	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: uint32(pid),
	}, nil
}

func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	// containerd will call Shutdown of sandbox to stop the sandbox
	return empty, nil
}

func (s *service) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	cgx := container.Cgroup()
	if cgx == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "cgroup does not exist")
	}
	var statsx interface{}
	switch cg := cgx.(type) {
	case cgroups.Cgroup:
		stats, err := cg.Stat(cgroups.IgnoreNotExist)
		if err != nil {
			return nil, err
		}
		statsx = stats
	case *cgroupsv2.Manager:
		stats, err := cg.Stat()
		if err != nil {
			return nil, err
		}
		statsx = stats
	default:
		return nil, errdefs.ToGRPCf(errdefs.ErrNotImplemented, "unsupported cgroup type %T", cg)
	}
	data, err := typeurl.MarshalAny(statsx)
	if err != nil {
		return nil, err
	}
	return &taskAPI.StatsResponse{
		Stats: protobuf.FromAny(data),
	}, nil
}

func (s *service) processExits() {
	for e := range s.ec {
		s.checkProcesses(e)
	}
}

func (s *service) send(evt interface{}) {
	s.events <- evt
}

func (s *service) sendL(evt interface{}) {
	s.eventSendMu.Lock()
	s.events <- evt
	s.eventSendMu.Unlock()
}

func (s *service) checkProcesses(e runcC.Exit) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, container := range s.containers {
		if !container.HasPid(e.Pid) {
			continue
		}

		for _, p := range container.All() {
			if p.Pid() != e.Pid {
				continue
			}

			if ip, ok := p.(*process.Init); ok {
				// Ensure all children are killed
				if runc.ShouldKillAllOnExit(s.context, container.Bundle) {
					if err := ip.KillAll(s.context); err != nil {
						logrus.WithError(err).WithField("id", ip.ID()).
							Error("failed to kill init's children")
					}
				}
			}
			logrus.Debugf("set exit status %v for process, %d, %s", e.Status, p.Pid(), p.ID())
			p.SetExited(e.Status)
			s.sendL(&eventstypes.TaskExit{
				ContainerID: container.ID,
				ID:          p.ID(),
				Pid:         uint32(e.Pid),
				ExitStatus:  uint32(e.Status),
				ExitedAt:    protobuf.ToTimestamp(p.ExitedAt()),
			})
			return
		}
		return
	}
}

func (s *service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	container, err := s.getContainer(id)
	if err != nil {
		return nil, err
	}
	p, err := container.Process("")
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	ps, err := p.(*process.Init).Runtime().Ps(ctx, id)
	if err != nil {
		return nil, err
	}
	pids := make([]uint32, 0, len(ps))
	for _, pid := range ps {
		pids = append(pids, uint32(pid))
	}
	return pids, nil
}

func (s *service) forward(ctx context.Context, publisher shim.Publisher) {
	ns, _ := namespaces.Namespace(ctx)
	ctx = namespaces.WithNamespace(context.Background(), ns)
	for e := range s.events {
		err := publisher.Publish(ctx, runc.GetTopic(e), e)
		if err != nil {
			logrus.WithError(err).Error("post event")
		}
	}
	publisher.Close()
}

func (s *service) getContainer(id string) (*runc.Container, error) {
	s.mu.Lock()
	container := s.containers[id]
	s.mu.Unlock()
	if container == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "container not created")
	}
	return container, nil
}

// initialize a single epoll fd to manage our consoles. `initPlatform` should
// only be called once.
func (s *service) initPlatform() error {
	if s.platform != nil {
		return nil
	}
	p, err := runc.NewPlatform()
	if err != nil {
		return err
	}
	s.platform = p
	return nil
}

func (s *service) recover(binary, root, path string) error {
	logrus.Debugf("recover runc containers from %s with binary %s and path %s", root, binary, path)
	defer func() {
		close(s.recoverFinished)
	}()
	for _, ns := range []string{"k8s.io", "moby", "default"} {
		runtime := process.NewRunc(root, path, ns, binary, false)
		containers, err := runtime.List(context.Background())
		if err != nil {
			return err
		}
		var existingContainers []*runc.Container
		for _, c := range containers {
			logrus.Debugf("found runc container %s", c.ID)
			cont, err := runc.RecoverContainer(runtime, c, ns, s.platform)
			if err != nil {
				return err
			}
			s.containers[c.ID] = cont
			existingContainers = append(existingContainers, cont)
		}

		go func() {
			for {
				if len(existingContainers) == 0 {
					return
				}
				currentContainers, err := runtime.List(context.Background())
				if err != nil {
					logrus.Warningf("failed to list containers when monitoring existing containers")
					return
				}
				var stillRunningContainers []*runc.Container
				for _, c := range existingContainers {
					running := false
					for _, newC := range currentContainers {
						if c.ID == newC.ID {
							p, err := c.Process("")
							if err != nil {
								logrus.Warningf("failed to get process of container %s", c.ID)
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
						stillRunningContainers = append(stillRunningContainers, c)
					}
				}
				existingContainers = stillRunningContainers
				time.Sleep(1 * time.Second)
			}
		}()
	}
	return nil
}

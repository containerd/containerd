//go:build linux

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

package task

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	eventstypes "github.com/containerd/containerd/api/events"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	runcC "github.com/containerd/go-runc"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/moby/sys/userns"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/core/runtime"
	oomv2 "github.com/containerd/containerd/v2/internal/oom"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oom"
	oomv1 "github.com/containerd/containerd/v2/pkg/oom/v1"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/containerd/v2/pkg/sys/reaper"
)

var (
	_     = shim.TTRPCService(&service{})
	empty = &ptypes.Empty{}
)

// NewTaskService creates a new instance of a task service
func NewTaskService(ctx context.Context, publisher shim.Publisher, sd shutdown.Service) (taskAPI.TTRPCTaskService, error) {
	var (
		ep  oom.Watcher
		err error
	)
	if cgroups.Mode() != cgroups.Unified {
		ep, err = oomv1.New(publisher)
		if err != nil {
			return nil, err
		}
		go ep.Run(ctx)
	}
	s := &service{
		context:              ctx,
		events:               make(chan interface{}, 128),
		ec:                   reaper.Default.Subscribe(),
		cg1oom:               ep,
		cg2oom:               oomv2.New(),
		publisher:            publisher,
		shutdown:             sd,
		containers:           make(map[string]*runc.Container),
		running:              make(map[int][]containerProcess),
		runningExecs:         make(map[*runc.Container]int),
		execCountSubscribers: make(map[*runc.Container]chan<- int),
		containerInitExit:    make(map[*runc.Container]runcC.Exit),
		exitSubscribers:      make(map[*map[int][]runcC.Exit]struct{}),
	}
	go s.processExits()
	runcC.Monitor = reaper.Default
	if err := s.initPlatform(); err != nil {
		return nil, fmt.Errorf("failed to initialized platform behavior: %w", err)
	}
	go s.forward(ctx, publisher)
	sd.RegisterCallback(func(context.Context) error {
		close(s.events)
		return nil
	})

	if address, err := shim.ReadAddress("address"); err == nil {
		sd.RegisterCallback(func(context.Context) error {
			return shim.RemoveSocket(address)
		})
	}
	return s, nil
}

// service is the shim implementation of a remote shim over GRPC
type service struct {
	mu sync.Mutex

	context  context.Context
	events   chan interface{}
	platform stdio.Platform
	ec       chan runcC.Exit
	cg1oom   oom.Watcher
	cg2oom   oomv2.Interface

	publisher events.Publisher

	containers map[string]*runc.Container

	lifecycleMu  sync.Mutex
	running      map[int][]containerProcess // pid -> running process, guarded by lifecycleMu
	runningExecs map[*runc.Container]int    // container -> num running execs, guarded by lifecycleMu
	// container -> subscription to exec exits/changes to s.runningExecs[container],
	// guarded by lifecycleMu
	execCountSubscribers map[*runc.Container]chan<- int
	// container -> init exits, guarded by lifecycleMu
	// Used to stash container init process exits, so that we can hold them
	// until after we've made sure to publish all the container's exec exits.
	// Also used to prevent starting new execs from being started if the
	// container's init process (read: pid, not [process.Init]) has already been
	// reaped by the shim.
	// Note that this flag gets updated before the container's [process.Init.Status]
	// is transitioned to "stopped".
	containerInitExit map[*runc.Container]runcC.Exit
	// Subscriptions to exits for PIDs. Adding/deleting subscriptions and
	// dereferencing the subscription pointers must only be done while holding
	// lifecycleMu.
	exitSubscribers map[*map[int][]runcC.Exit]struct{}

	shutdown shutdown.Service
}

type containerProcess struct {
	Container *runc.Container
	Process   process.Process
}

// preStart prepares for starting a container process and handling its exit.
// The container being started should be passed in as c when starting the container
// init process for an already-created container. c should be nil when creating a
// container or when starting an exec.
//
// The returned handleStarted closure records that the process has started so
// that its exit can be handled efficiently. If the process has already exited,
// it handles the exit immediately.
// handleStarted should be called after the event announcing the start of the
// process has been published. Note that s.lifecycleMu must not be held when
// calling handleStarted.
//
// The returned cleanup closure releases resources used to handle early exits.
// It must be called before the caller of preStart returns, otherwise severe
// memory leaks will occur.
func (s *service) preStart(c *runc.Container) (handleStarted func(*runc.Container, process.Process), cleanup func()) {
	exits := make(map[int][]runcC.Exit)
	s.exitSubscribers[&exits] = struct{}{}

	if c != nil {
		// Remove container init process from s.running so it will once again be
		// treated as an early exit if it exits before handleStarted is called.
		pid := c.Pid()
		var newRunning []containerProcess
		for _, cp := range s.running[pid] {
			if cp.Container != c {
				newRunning = append(newRunning, cp)
			}
		}
		if len(newRunning) > 0 {
			s.running[pid] = newRunning
		} else {
			delete(s.running, pid)
		}
	}

	handleStarted = func(c *runc.Container, p process.Process) {
		var pid int
		if p != nil {
			pid = p.Pid()
		}

		s.lifecycleMu.Lock()

		ees, exited := exits[pid]
		delete(s.exitSubscribers, &exits)
		exits = nil
		if pid == 0 || exited {
			s.lifecycleMu.Unlock()
			for _, ee := range ees {
				s.handleProcessExit(ee, c, p)
			}
		} else {
			// Process start was successful, add to `s.running`.
			s.running[pid] = append(s.running[pid], containerProcess{
				Container: c,
				Process:   p,
			})
			s.lifecycleMu.Unlock()
		}
	}

	cleanup = func() {
		if exits != nil {
			s.lifecycleMu.Lock()
			defer s.lifecycleMu.Unlock()
			delete(s.exitSubscribers, &exits)
		}
	}

	return handleStarted, cleanup
}

// Create a new initial process and container with the underlying OCI runtime
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (_ *taskAPI.CreateTaskResponse, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lifecycleMu.Lock()
	handleStarted, cleanup := s.preStart(nil)
	s.lifecycleMu.Unlock()
	defer cleanup()

	container, err := runc.NewContainer(ctx, s.platform, r)
	if err != nil {
		return nil, err
	}

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

	// After runc.Create(init), the container’s cgroup contains a paused init process.
	// Therefore, we should start monitoring OOM events immediately after creation, in
	// case the process goes OOM very quickly. Otherwise, we may encounter flaky cases
	switch cg := container.Cgroup().(type) {
	case cgroup1.Cgroup:
		if err := s.cg1oom.Add(container.ID, cg); err != nil {
			log.G(ctx).WithError(err).Error("add cg to OOM monitor")
		}
	case *cgroupsv2.Manager:
		allControllers, err := cg.RootControllers()
		if err != nil {
			log.G(ctx).WithError(err).Error("failed to get root controllers")
		} else {
			if err := cg.ToggleControllers(allControllers, cgroupsv2.Enable); err != nil {
				if userns.RunningInUserNS() {
					log.G(ctx).WithError(err).Debugf("failed to enable controllers (%v)", allControllers)
				} else {
					log.G(ctx).WithError(err).Errorf("failed to enable controllers (%v)", allControllers)
				}
			}
		}

		if err := s.cg2oom.Add(container.ID, container.Pid(), s.oomEvent); err != nil {
			log.G(ctx).WithError(err).WithField("container_id", container.ID).Error("failed to watch oom events")
		}
	}

	// The following line cannot return an error as the only state in which that
	// could happen would also cause the container.Pid() call above to
	// nil-deference panic.
	proc, _ := container.Process("")
	handleStarted(container, proc)

	return &taskAPI.CreateTaskResponse{
		Pid: uint32(container.Pid()),
	}, nil
}

func (s *service) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTTRPCTaskService(server, s)
	return nil
}

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}

	var cinit *runc.Container
	s.lifecycleMu.Lock()
	if r.ExecID == "" {
		cinit = container
	} else {
		if _, initExited := s.containerInitExit[container]; initExited {
			s.lifecycleMu.Unlock()
			return nil, errgrpc.ToGRPCf(errdefs.ErrFailedPrecondition, "container %s init process is not running", container.ID)
		}
		s.runningExecs[container]++
	}
	handleStarted, cleanup := s.preStart(cinit)
	s.lifecycleMu.Unlock()
	defer cleanup()

	p, err := container.Start(ctx, r)
	if err != nil {
		// If we failed to even start the process, s.runningExecs
		// won't get decremented in s.handleProcessExit. We still need
		// to update it.
		if r.ExecID != "" {
			s.lifecycleMu.Lock()
			s.runningExecs[container]--
			if ch, ok := s.execCountSubscribers[container]; ok {
				ch <- s.runningExecs[container]
			}
			s.lifecycleMu.Unlock()
		}
		handleStarted(container, p)
		return nil, errgrpc.ToGRPC(err)
	}

	switch r.ExecID {
	case "":
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
	handleStarted(container, p)
	return &taskAPI.StartResponse{
		Pid: uint32(p.Pid()),
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Delete(ctx, r)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
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
		s.lifecycleMu.Lock()
		delete(s.containerInitExit, container)
		s.lifecycleMu.Unlock()
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
		return nil, errgrpc.ToGRPCf(errdefs.ErrAlreadyExists, "id %s", r.ExecID)
	}
	process, err := container.Exec(ctx, r)
	if err != nil {
		cancel()
		return nil, errgrpc.ToGRPC(err)
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
		return nil, errgrpc.ToGRPC(err)
	}
	return empty, nil
}

// State returns runtime state information for a process
func (s *service) State(ctx context.Context, r *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Process(r.ExecID)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
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
		return nil, errgrpc.ToGRPC(err)
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
		return nil, errgrpc.ToGRPC(err)
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
		return nil, errgrpc.ToGRPC(err)
	}
	return empty, nil
}

// Pids returns all pids inside the container
func (s *service) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	pids, err := s.getContainerPids(ctx, container)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
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
				a, err := typeurl.MarshalAnyToProto(d)
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
		return nil, errgrpc.ToGRPC(err)
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
		return nil, errgrpc.ToGRPC(err)
	}
	return empty, nil
}

// Wait for a process to exit
func (s *service) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	p, err := container.Process(r.ExecID)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	if err = p.Wait(ctx); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &taskAPI.WaitResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   protobuf.ToTimestamp(p.ExitedAt()),
	}, nil
}

// Connect returns shim information such as the shim's pid
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	var pid int
	if container, err := s.getContainer(r.ID); err == nil {
		pid = container.Pid()
	}
	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: uint32(pid),
	}, nil
}

func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// return out if the shim is still servicing containers
	if len(s.containers) > 0 {
		return empty, nil
	}

	// please make sure that temporary resource has been cleanup or registered
	// for cleanup before calling shutdown
	s.shutdown.Shutdown()

	return empty, nil
}

func (s *service) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	container, err := s.getContainer(r.ID)
	if err != nil {
		return nil, err
	}
	cgx := container.Cgroup()
	if cgx == nil {
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotFound, "cgroup does not exist")
	}
	var statsx interface{}
	switch cg := cgx.(type) {
	case cgroup1.Cgroup:
		stats, err := cg.Stat(cgroup1.IgnoreNotExist)
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
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotImplemented, "unsupported cgroup type %T", cg)
	}
	data, err := typeurl.MarshalAny(statsx)
	if err != nil {
		return nil, err
	}
	return &taskAPI.StatsResponse{
		Stats: typeurl.MarshalProto(data),
	}, nil
}

func (s *service) processExits() {
	for e := range s.ec {
		// While unlikely, it is not impossible for a container process to exit
		// and have its PID be recycled for a new container process before we
		// have a chance to process the first exit. As we have no way to tell
		// for sure which of the processes the exit event corresponds to (until
		// pidfd support is implemented) there is no way for us to handle the
		// exit correctly in that case.

		s.lifecycleMu.Lock()
		// Inform any concurrent s.Start() calls so they can handle the exit
		// if the PID belongs to them.
		for subscriber := range s.exitSubscribers {
			(*subscriber)[e.Pid] = append((*subscriber)[e.Pid], e)
		}
		// Handle the exit for a created/started process. If there's more than
		// one, assume they've all exited. One of them will be the correct
		// process.
		var cps []containerProcess
		for _, cp := range s.running[e.Pid] {
			_, init := cp.Process.(*process.Init)
			if init {
				s.containerInitExit[cp.Container] = e
			}
			cps = append(cps, cp)
		}
		delete(s.running, e.Pid)
		s.lifecycleMu.Unlock()

		for _, cp := range cps {
			if ip, ok := cp.Process.(*process.Init); ok {
				s.handleInitExit(e, cp.Container, ip)
			} else {
				s.handleProcessExit(e, cp.Container, cp.Process)
			}
		}
	}
}

func (s *service) oomEvent(id string) {
	err := s.publisher.Publish(s.context, runtime.TaskOOMEventTopic, &eventstypes.TaskOOM{
		ContainerID: id,
	})
	if err != nil {
		log.G(s.context).WithError(err).Error("post event")
	}
}

func (s *service) send(evt interface{}) {
	s.events <- evt
}

// handleInitExit processes container init process exits.
// This is handled separately from non-init exits, because there
// are some extra invariants we want to ensure in this case, namely:
// - for a given container, the init process exit MUST be the last exit published
// This is achieved by:
// - killing all running container processes (if the container has a shared pid
// namespace, otherwise all other processes have been reaped already).
// - waiting for the container's running exec counter to reach 0.
// - finally, publishing the init exit.
func (s *service) handleInitExit(e runcC.Exit, c *runc.Container, p *process.Init) {
	// kill all running container processes
	if runc.ShouldKillAllOnExit(s.context, c.Bundle) {
		if err := p.KillAll(s.context); err != nil {
			log.G(s.context).WithError(err).WithField("id", p.ID()).
				Error("failed to kill init's children")
		}
	}

	s.lifecycleMu.Lock()
	numRunningExecs := s.runningExecs[c]
	if numRunningExecs == 0 {
		delete(s.runningExecs, c)
		s.lifecycleMu.Unlock()
		s.handleProcessExit(e, c, p)
		return
	}

	events := make(chan int, numRunningExecs)
	s.execCountSubscribers[c] = events

	s.lifecycleMu.Unlock()

	go func() {
		defer func() {
			s.lifecycleMu.Lock()
			defer s.lifecycleMu.Unlock()
			delete(s.execCountSubscribers, c)
			delete(s.runningExecs, c)
		}()

		// wait for running processes to exit
		for {
			if runningExecs := <-events; runningExecs == 0 {
				break
			}
		}

		// all running processes have exited now, and no new
		// ones can start, so we can publish the init exit
		s.handleProcessExit(e, c, p)
	}()
}

func (s *service) handleProcessExit(e runcC.Exit, c *runc.Container, p process.Process) {
	p.SetExited(e.Status)
	_, isInit := p.(*process.Init)
	if isInit {
		if err := s.cg2oom.Stop(c.ID); err != nil {
			log.G(context.Background()).
				WithField("container_id", c.ID).
				WithError(err).
				Error("failed to stop oom event watcher")
		}
	}
	s.send(&eventstypes.TaskExit{
		ContainerID: c.ID,
		ID:          p.ID(),
		Pid:         uint32(e.Pid),
		ExitStatus:  uint32(e.Status),
		ExitedAt:    protobuf.ToTimestamp(p.ExitedAt()),
	})
	if !isInit {
		s.lifecycleMu.Lock()
		s.runningExecs[c]--
		if ch, ok := s.execCountSubscribers[c]; ok {
			ch <- s.runningExecs[c]
		}
		s.lifecycleMu.Unlock()
	}
}

func (s *service) getContainerPids(ctx context.Context, container *runc.Container) ([]uint32, error) {
	p, err := container.Process("")
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	ps, err := p.(*process.Init).Runtime().Ps(ctx, container.ID)
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
		err := publisher.Publish(ctx, runtime.GetTopic(e), e)
		if err != nil {
			log.G(ctx).WithError(err).Error("post event")
		}
	}
	publisher.Close()
}

func (s *service) getContainer(id string) (*runc.Container, error) {
	s.mu.Lock()
	container := s.containers[id]
	s.mu.Unlock()
	if container == nil {
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotFound, "container not created")
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
	s.shutdown.RegisterCallback(func(context.Context) error { return s.platform.Close() })
	return nil
}

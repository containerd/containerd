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

package v1

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/cgroups"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/oom"
	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/pkg/stdio"
	"github.com/containerd/containerd/runtime/v2/runc"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/containerd/runtime/v2/shim"
	taskAPI "github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/containerd/sys/reaper"
	runcC "github.com/containerd/go-runc"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	ptypes "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	_     = (taskAPI.TaskService)(&service{})
	empty = &ptypes.Empty{}
)

// New returns a new shim service that can be used via GRPC
func New(ctx context.Context, id string, publisher shim.Publisher, shutdown func()) (shim.Shim, error) {
	ep, err := oom.New(publisher)
	if err != nil {
		return nil, err
	}
	go ep.Run(ctx)
	s := &service{
		id:      id,
		context: ctx,
		events:  make(chan interface{}, 128),
		ec:      reaper.Default.Subscribe(),
		ep:      ep,
		cancel:  shutdown,
	}
	go s.processExits()
	runcC.Monitor = reaper.Default
	if err := s.initPlatform(); err != nil {
		shutdown()
		return nil, errors.Wrap(err, "failed to initialized platform behavior")
	}
	go s.forward(ctx, publisher)
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
	ep       *oom.Epoller

	id        string
	container *runc.Container

	cancel func()
}

func newCommand(ctx context.Context, id, containerdBinary, containerdAddress string) (*exec.Cmd, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-namespace", ns,
		"-id", id,
		"-address", containerdAddress,
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd, nil
}

func (s *service) StartShim(ctx context.Context, id, containerdBinary, containerdAddress string) (string, error) {
	cmd, err := newCommand(ctx, id, containerdBinary, containerdAddress)
	if err != nil {
		return "", err
	}
	address, err := shim.SocketAddress(ctx, id)
	if err != nil {
		return "", err
	}
	socket, err := shim.NewSocket(address)
	if err != nil {
		return "", err
	}
	defer socket.Close()
	f, err := socket.File()
	if err != nil {
		return "", err
	}
	defer f.Close()

	cmd.ExtraFiles = append(cmd.ExtraFiles, f)

	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			cmd.Process.Kill()
		}
	}()
	// make sure to wait after start
	go cmd.Wait()
	if err := shim.WritePidFile("shim.pid", cmd.Process.Pid); err != nil {
		return "", err
	}
	if err := shim.WriteAddress("address", address); err != nil {
		return "", err
	}
	if data, err := ioutil.ReadAll(os.Stdin); err == nil {
		if len(data) > 0 {
			var any types.Any
			if err := proto.Unmarshal(data, &any); err != nil {
				return "", err
			}
			v, err := typeurl.UnmarshalAny(&any)
			if err != nil {
				return "", err
			}
			if opts, ok := v.(*options.Options); ok {
				if opts.ShimCgroup != "" {
					cg, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(opts.ShimCgroup))
					if err != nil {
						return "", errors.Wrapf(err, "failed to load cgroup %s", opts.ShimCgroup)
					}
					if err := cg.Add(cgroups.Process{
						Pid: cmd.Process.Pid,
					}); err != nil {
						return "", errors.Wrapf(err, "failed to join cgroup %s", opts.ShimCgroup)
					}
				}
			}
		}
	}
	if err := shim.AdjustOOMScore(cmd.Process.Pid); err != nil {
		return "", errors.Wrap(err, "failed to adjust OOM score for shim")
	}
	return address, nil
}

func (s *service) Cleanup(ctx context.Context) (*taskAPI.DeleteResponse, error) {
	path, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	runtime, err := runc.ReadRuntime(path)
	if err != nil {
		return nil, err
	}
	r := process.NewRunc(process.RuncRoot, path, ns, runtime, "", false)
	if err := r.Delete(ctx, s.id, &runcC.DeleteOpts{
		Force: true,
	}); err != nil {
		logrus.WithError(err).Warn("failed to remove runc container")
	}
	if err := mount.UnmountAll(filepath.Join(path, "rootfs"), 0); err != nil {
		logrus.WithError(err).Warn("failed to cleanup rootfs mount")
	}
	return &taskAPI.DeleteResponse{
		ExitedAt:   time.Now(),
		ExitStatus: 128 + uint32(unix.SIGKILL),
	}, nil
}

// Create a new initial process and container with the underlying OCI runtime
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (_ *taskAPI.CreateTaskResponse, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	container, err := runc.NewContainer(ctx, s.platform, r)
	if err != nil {
		return nil, err
	}

	s.container = container

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

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	container, err := s.getContainer()
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
	if err := s.ep.Add(container.ID, container.Cgroup()); err != nil {
		logrus.WithError(err).Error("add cg to OOM monitor")
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
	s.eventSendMu.Unlock()
	return &taskAPI.StartResponse{
		Pid: uint32(p.Pid()),
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	container, err := s.getContainer()
	if err != nil {
		return nil, err
	}
	p, err := container.Delete(ctx, r)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	// if we deleted our init task, close the platform and send the task delete event
	if r.ExecID == "" {
		if s.platform != nil {
			s.platform.Close()
		}
		s.send(&eventstypes.TaskDelete{
			ContainerID: container.ID,
			Pid:         uint32(p.Pid()),
			ExitStatus:  uint32(p.ExitStatus()),
			ExitedAt:    p.ExitedAt(),
		})
	}
	return &taskAPI.DeleteResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
		Pid:        uint32(p.Pid()),
	}, nil
}

// Exec an additional process inside the container
func (s *service) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer()
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
		ContainerID: s.container.ID,
		ExecID:      process.ID(),
	})
	return empty, nil
}

// ResizePty of a process
func (s *service) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer()
	if err != nil {
		return nil, err
	}
	if err := container.ResizePty(ctx, r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return empty, nil
}

// State returns runtime state information for a process
func (s *service) State(ctx context.Context, r *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	p, err := s.getProcess(r.ExecID)
	if err != nil {
		return nil, err
	}
	st, err := p.Status(ctx)
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
	case "pausing":
		status = task.StatusPausing
	}
	sio := p.Stdio()
	return &taskAPI.StateResponse{
		ID:         p.ID(),
		Bundle:     s.container.Bundle,
		Pid:        uint32(p.Pid()),
		Status:     status,
		Stdin:      sio.Stdin,
		Stdout:     sio.Stdout,
		Stderr:     sio.Stderr,
		Terminal:   sio.Terminal,
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
	}, nil
}

// Pause the container
func (s *service) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	container, err := s.getContainer()
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
	container, err := s.getContainer()
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
	container, err := s.getContainer()
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
	container, err := s.getContainer()
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
				a, err := typeurl.MarshalAny(d)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to marshal process %d info", pid)
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
	container, err := s.getContainer()
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
	container, err := s.getContainer()
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
	container, err := s.getContainer()
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
	container, err := s.getContainer()
	if err != nil {
		return nil, err
	}
	p, err := container.Process(r.ExecID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	p.Wait()

	return &taskAPI.WaitResponse{
		ExitStatus: uint32(p.ExitStatus()),
		ExitedAt:   p.ExitedAt(),
	}, nil
}

// Connect returns shim information such as the shim's pid
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	var pid int
	if s.container != nil {
		pid = s.container.Pid()
	}
	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: uint32(pid),
	}, nil
}

func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	s.cancel()
	close(s.events)
	return empty, nil
}

func (s *service) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	cg := s.container.Cgroup()
	if cg == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "cgroup does not exist")
	}
	stats, err := cg.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		return nil, err
	}
	data, err := typeurl.MarshalAny(stats)
	if err != nil {
		return nil, err
	}
	return &taskAPI.StatsResponse{
		Stats: data,
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
	container, err := s.getContainer()
	if err != nil {
		return
	}

	shouldKillAll, err := shouldKillAllOnExit(container.Bundle)
	if err != nil {
		log.G(s.context).WithError(err).Error("failed to check shouldKillAll")
	}

	for _, p := range container.All() {
		if p.Pid() == e.Pid {
			if shouldKillAll {
				if ip, ok := p.(*process.Init); ok {
					// Ensure all children are killed
					if err := ip.KillAll(s.context); err != nil {
						logrus.WithError(err).WithField("id", ip.ID()).
							Error("failed to kill init's children")
					}
				}
			}
			p.SetExited(e.Status)
			s.sendL(&eventstypes.TaskExit{
				ContainerID: container.ID,
				ID:          p.ID(),
				Pid:         uint32(e.Pid),
				ExitStatus:  uint32(e.Status),
				ExitedAt:    p.ExitedAt(),
			})
			return
		}
	}
}

func shouldKillAllOnExit(bundlePath string) (bool, error) {
	var bundleSpec specs.Spec
	bundleConfigContents, err := ioutil.ReadFile(filepath.Join(bundlePath, "config.json"))
	if err != nil {
		return false, err
	}
	json.Unmarshal(bundleConfigContents, &bundleSpec)

	if bundleSpec.Linux != nil {
		for _, ns := range bundleSpec.Linux.Namespaces {
			if ns.Type == specs.PIDNamespace {
				return false, nil
			}
		}
	}

	return true, nil
}

func (s *service) getContainerPids(ctx context.Context, id string) ([]uint32, error) {
	p, err := s.container.Process("")
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
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := publisher.Publish(ctx, runc.GetTopic(e), e)
		cancel()
		if err != nil {
			logrus.WithError(err).Error("post event")
		}
	}
	publisher.Close()
}

func (s *service) getContainer() (*runc.Container, error) {
	s.mu.Lock()
	container := s.container
	s.mu.Unlock()
	if container == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "container not created")
	}
	return container, nil
}

func (s *service) getProcess(execID string) (process.Process, error) {
	container, err := s.getContainer()
	if err != nil {
		return nil, err
	}
	p, err := container.Process(execID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return p, nil
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

package shim

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const (
	DefaultShimBinary = "containerd-shim"

	pidFilename         = "pid"
	startTimeFilename   = "starttime"
	exitPipeFilename    = "exit"
	controlPipeFilename = "control"
	exitStatusFilename  = "exitStatus"
	shimLogFileName     = "shim-log.json"
)

func New(ctx context.Context, root, shim, runtime string, runtimeArgs []string) (*ShimRuntime, error) {
	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, errors.Wrap(err, "epollcreate1 failed")
	}
	s := &ShimRuntime{
		ctx:          ctx,
		epollFd:      fd,
		root:         root,
		binaryName:   shim,
		runtime:      runtime,
		runtimeArgs:  runtimeArgs,
		exitChannels: make(map[int]*process),
		containers:   make(map[string]*execution.Container),
	}

	s.loadContainers()

	go s.monitor()

	return s, nil
}

type ShimRuntime struct {
	ctx context.Context

	mutex        sync.Mutex
	exitChannels map[int]*process
	containers   map[string]*execution.Container

	epollFd     int
	root        string
	binaryName  string
	runtime     string
	runtimeArgs []string
}

type ProcessOpts struct {
	Bundle   string
	Terminal bool
	Stdin    string
	Stdout   string
	Stderr   string
}

type processState struct {
	specs.Process
	Exec           bool     `json:"exec"`
	Stdin          string   `json:"containerdStdin"`
	Stdout         string   `json:"containerdStdout"`
	Stderr         string   `json:"containerdStderr"`
	RuntimeArgs    []string `json:"runtimeArgs"`
	NoPivotRoot    bool     `json:"noPivotRoot"`
	CheckpointPath string   `json:"checkpoint"`
	RootUID        int      `json:"rootUID"`
	RootGID        int      `json:"rootGID"`
}

func (s *ShimRuntime) Create(ctx context.Context, id string, o execution.CreateOpts) (*execution.Container, error) {
	log.G(s.ctx).WithFields(logrus.Fields{"container-id": id, "options": o}).Debug("Create()")

	if s.getContainer(id) != nil {
		return nil, execution.ErrContainerExists
	}

	containerCtx := log.WithModule(log.WithModule(ctx, "container"), id)
	container, err := execution.NewContainer(containerCtx, filepath.Join(s.root, id), id, o.Bundle)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			container.Cleanup()
		}
	}()

	// extract Process spec from bundle's config.json
	var spec specs.Spec
	f, err := os.Open(filepath.Join(o.Bundle, "config.json"))
	if err != nil {
		return nil, errors.Wrap(err, "failed to open config.json")
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&spec); err != nil {
		return nil, errors.Wrap(err, "failed to decode container OCI specs")
	}

	processOpts := newProcessOpts{
		shimBinary:  s.binaryName,
		runtime:     s.runtime,
		runtimeArgs: s.runtimeArgs,
		container:   container,
		exec:        false,
		stateDir:    container.ProcessStateDir(execution.InitProcessID),
		StartProcessOpts: execution.StartProcessOpts{
			ID:      execution.InitProcessID,
			Spec:    spec.Process,
			Console: o.Console,
			Stdin:   o.Stdin,
			Stdout:  o.Stdout,
			Stderr:  o.Stderr,
		},
	}

	processCtx := log.WithModule(log.WithModule(containerCtx, "process"), execution.InitProcessID)
	process, err := newProcess(processCtx, processOpts)
	if err != nil {
		return nil, err
	}

	s.monitorProcess(process)
	container.AddProcess(process)

	s.addContainer(container)

	return container, nil
}

func (s *ShimRuntime) Start(ctx context.Context, c *execution.Container) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c}).Debug("Start()")

	cmd := exec.CommandContext(ctx, s.runtime, append(s.runtimeArgs, "start", c.ID())...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "'%s start' failed with output: %v", s.runtime, string(out))
	}
	return nil
}

func (s *ShimRuntime) List(ctx context.Context) ([]*execution.Container, error) {
	log.G(s.ctx).Debug("List()")

	containers := make([]*execution.Container, 0)
	s.mutex.Lock()
	for _, c := range s.containers {
		containers = append(containers, c)
	}
	s.mutex.Unlock()

	return containers, nil
}

func (s *ShimRuntime) Load(ctx context.Context, id string) (*execution.Container, error) {
	log.G(s.ctx).WithFields(logrus.Fields{"container-id": id}).Debug("Start()")

	s.mutex.Lock()
	c, ok := s.containers[id]
	s.mutex.Unlock()

	if !ok {
		return nil, errors.New(execution.ErrContainerNotFound.Error())
	}

	return c, nil
}

func (s *ShimRuntime) Delete(ctx context.Context, c *execution.Container) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c}).Debug("Delete()")

	if c.Status() != execution.Stopped {
		return errors.Errorf("cannot delete a container in the '%s' state", c.Status())
	}

	c.Cleanup()
	s.removeContainer(c)
	return nil
}

func (s *ShimRuntime) Pause(ctx context.Context, c *execution.Container) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c}).Debug("Pause()")

	cmd := exec.CommandContext(ctx, s.runtime, append(s.runtimeArgs, "pause", c.ID())...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "'%s pause' failed with output: %v", s.runtime, string(out))
	}
	return nil
}

func (s *ShimRuntime) Resume(ctx context.Context, c *execution.Container) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c}).Debug("Resume()")

	cmd := exec.CommandContext(ctx, s.runtime, append(s.runtimeArgs, "resume", c.ID())...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "'%s resume' failed with output: %v", s.runtime, string(out))
	}
	return nil
}

func (s *ShimRuntime) StartProcess(ctx context.Context, c *execution.Container, o execution.StartProcessOpts) (p execution.Process, err error) {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c, "options": o}).Debug("StartProcess()")

	processOpts := newProcessOpts{
		shimBinary:       s.binaryName,
		runtime:          s.runtime,
		runtimeArgs:      s.runtimeArgs,
		container:        c,
		exec:             true,
		stateDir:         c.ProcessStateDir(o.ID),
		StartProcessOpts: o,
	}

	processCtx := log.WithModule(log.WithModule(c.Context(), "process"), execution.InitProcessID)
	process, err := newProcess(processCtx, processOpts)
	if err != nil {
		return nil, err
	}

	process.status = execution.Running
	s.monitorProcess(process)

	c.AddProcess(process)
	return process, nil
}

func (s *ShimRuntime) SignalProcess(ctx context.Context, c *execution.Container, id string, sig os.Signal) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c, "process-id": id, "signal": sig}).
		Debug("SignalProcess()")

	process := c.GetProcess(id)
	if process == nil {
		return errors.Errorf("container %s has no process named %s", c.ID(), id)
	}
	err := syscall.Kill(int(process.Pid()), sig.(syscall.Signal))
	if err != nil {
		return errors.Wrapf(err, "failed to send %v signal to container %s process %v", sig, c.ID(), process.Pid())
	}
	return err
}

func (s *ShimRuntime) DeleteProcess(ctx context.Context, c *execution.Container, id string) error {
	log.G(s.ctx).WithFields(logrus.Fields{"container": c, "process-id": id}).
		Debug("DeleteProcess()")

	if p := c.GetProcess(id); p != nil {
		p.(*process).cleanup()

		return c.RemoveProcess(id)
	}

	return errors.Errorf("container %s has no process named %s", c.ID(), id)
}

func (s *ShimRuntime) monitor() {
	var events [128]syscall.EpollEvent
	for {
		n, err := syscall.EpollWait(s.epollFd, events[:], -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			log.G(s.ctx).Error("epollwait failed:", err)
		}

		for i := 0; i < n; i++ {
			fd := int(events[i].Fd)

			s.mutex.Lock()
			p := s.exitChannels[fd]
			delete(s.exitChannels, fd)
			s.mutex.Unlock()

			if err = syscall.EpollCtl(s.epollFd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{
				Events: syscall.EPOLLHUP,
				Fd:     int32(fd),
			}); err != nil {
				log.G(s.ctx).Error("epollctl deletion failed:", err)
			}

			close(p.exitChan)
		}
	}
}

func (s *ShimRuntime) addContainer(c *execution.Container) {
	s.mutex.Lock()
	s.containers[c.ID()] = c
	s.mutex.Unlock()
}

func (s *ShimRuntime) removeContainer(c *execution.Container) {
	s.mutex.Lock()
	delete(s.containers, c.ID())
	s.mutex.Unlock()
}

func (s *ShimRuntime) getContainer(id string) *execution.Container {
	s.mutex.Lock()
	c := s.containers[id]
	s.mutex.Unlock()

	return c
}

// monitorProcess adds a process to the list of monitored process if
// we fail to do so, we closed the exitChan channel used by Wait().
// Since service always call on Wait() for generating "exit" events,
// this will ensure the process gets killed
func (s *ShimRuntime) monitorProcess(p *process) {
	if p.status == execution.Stopped {
		close(p.exitChan)
		return
	}

	fd := int(p.exitPipe.Fd())
	event := syscall.EpollEvent{
		Fd:     int32(fd),
		Events: syscall.EPOLLHUP,
	}
	s.mutex.Lock()
	s.exitChannels[fd] = p
	s.mutex.Unlock()
	if err := syscall.EpollCtl(s.epollFd, syscall.EPOLL_CTL_ADD, fd, &event); err != nil {
		s.mutex.Lock()
		delete(s.exitChannels, fd)
		s.mutex.Unlock()
		close(p.exitChan)
		return
	}

	// TODO: take care of the OOM handler
}

func (s *ShimRuntime) unmonitorProcess(p *process) {
	s.mutex.Lock()
	for fd, proc := range s.exitChannels {
		if proc == p {
			delete(s.exitChannels, fd)
			break
		}
	}
	s.mutex.Unlock()
}

func (s *ShimRuntime) loadContainers() {
	cs, err := ioutil.ReadDir(s.root)
	if err != nil {
		log.G(s.ctx).WithField("statedir", s.root).
			Warn("failed to load containers, state dir cannot be listed:", err)
		return
	}

	for _, c := range cs {
		if !c.IsDir() {
			continue
		}

		stateDir := filepath.Join(s.root, c.Name())
		containerCtx := log.WithModule(log.WithModule(s.ctx, "container"), c.Name())
		container, err := execution.LoadContainer(containerCtx, stateDir, c.Name())
		if err != nil {
			log.G(s.ctx).WithField("container-id", c.Name()).Warn(err)
			continue
		}

		processDirs, err := container.ProcessesStateDir()
		if err != nil {
			log.G(s.ctx).WithField("container-id", c.Name()).Warn(err)
			continue
		}

		for processID, processStateDir := range processDirs {
			processCtx := log.WithModule(log.WithModule(containerCtx, "process"), processID)
			var p *process
			p, err = loadProcess(processCtx, processStateDir, processID)
			if err != nil {
				log.G(s.ctx).WithFields(logrus.Fields{"container-id": c.Name(), "process": processID}).Warn(err)
				break
			}
			if processID == execution.InitProcessID && p.status == execution.Running {
				p.status = s.loadContainerStatus(container.ID())
			}
			container.AddProcess(p)
		}

		// if successfull, add the container to our list
		if err == nil {
			for _, p := range container.Processes() {
				s.monitorProcess(p.(*process))
			}
			s.addContainer(container)
			log.G(s.ctx).Infof("restored container %s", container.ID())
		}
	}
}

func (s *ShimRuntime) loadContainerStatus(id string) execution.Status {
	cmd := exec.Command(s.runtime, append(s.runtimeArgs, "state", id)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return execution.Unknown
	}

	var st struct{ Status string }
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&st); err != nil {
		return execution.Unknown
	}

	return execution.Status(st.Status)
}

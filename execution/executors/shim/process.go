package shim

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/log"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	runc "github.com/crosbymichael/go-runc"
	starttime "github.com/opencontainers/runc/libcontainer/system"
)

type newProcessOpts struct {
	shimBinary  string
	runtime     string
	runtimeArgs []string
	container   *execution.Container
	exec        bool
	stateDir    string
	execution.StartProcessOpts
}

func newProcess(ctx context.Context, o newProcessOpts) (p *process, err error) {
	p = &process{
		id:       o.ID,
		stateDir: o.stateDir,
		exitChan: make(chan struct{}),
		ctx:      ctx,
	}
	defer func() {
		if err != nil {
			p.cleanup()
			p = nil
		}
	}()

	if err = os.Mkdir(o.stateDir, 0700); err != nil {
		err = errors.Wrap(err, "failed to create process state dir")
		return
	}

	p.exitPipe, p.controlPipe, err = getControlPipes(o.stateDir)
	if err != nil {
		return
	}

	cmd, err := newShimProcess(o)
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	abortCh := make(chan syscall.WaitStatus, 1)
	go func() {
		var shimStatus syscall.WaitStatus
		if err := cmd.Wait(); err != nil {
			shimStatus = execution.UnknownStatusCode
		} else {
			shimStatus = cmd.ProcessState.Sys().(syscall.WaitStatus)
		}
		abortCh <- shimStatus
		close(abortCh)
	}()

	p.pid, p.startTime, p.status, err = waitUntilReady(ctx, abortCh, o.stateDir)
	if err != nil {
		return
	}

	return
}

func loadProcess(ctx context.Context, stateDir, id string) (p *process, err error) {
	p = &process{
		id:       id,
		stateDir: stateDir,
		exitChan: make(chan struct{}),
		status:   execution.Running,
		ctx:      ctx,
	}
	defer func() {
		if err != nil {
			p.cleanup()
			p = nil
		}
	}()

	p.pid, err = getPidFromFile(filepath.Join(stateDir, pidFilename))
	if err != nil {
		err = errors.Wrap(err, "failed to read pid")
		return
	}

	p.startTime, err = getStartTimeFromFile(filepath.Join(stateDir, startTimeFilename))
	if err != nil {
		return
	}

	path := filepath.Join(stateDir, exitPipeFilename)
	p.exitPipe, err = os.OpenFile(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		err = errors.Wrapf(err, "failed to open exit pipe")
		return
	}

	path = filepath.Join(stateDir, controlPipeFilename)
	p.controlPipe, err = os.OpenFile(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		err = errors.Wrapf(err, "failed to open control pipe")
		return
	}

	markAsStopped := func(p *process) (*process, error) {
		p.setStatus(execution.Stopped)
		return p, nil
	}

	if err = syscall.Kill(int(p.pid), 0); err != nil {
		if err == syscall.ESRCH {
			return markAsStopped(p)
		}
		err = errors.Wrapf(err, "failed to check if process is still alive")
		return
	}

	cstime, err := starttime.GetProcessStartTime(int(p.pid))
	if err != nil {
		if os.IsNotExist(err) {
			return markAsStopped(p)
		}
		err = errors.Wrapf(err, "failed retrieve current process start time")
		return
	}

	if p.startTime != cstime {
		return markAsStopped(p)
	}

	return
}

type process struct {
	stateDir    string
	id          string
	pid         int64
	exitChan    chan struct{}
	exitPipe    *os.File
	controlPipe *os.File
	startTime   string
	status      execution.Status
	ctx         context.Context
	mu          sync.Mutex
}

func (p *process) ID() string {
	return p.id
}

func (p *process) Pid() int64 {
	return p.pid
}

func (p *process) Wait() (uint32, error) {
	<-p.exitChan

	log.G(p.ctx).WithFields(logrus.Fields{"process-id": p.ID(), "pid": p.pid}).
		Debugf("wait is over")

	// Cleanup those fds
	p.exitPipe.Close()
	p.controlPipe.Close()

	// If the container process is still alive, it means the shim crashed
	// and the child process had updated it PDEATHSIG to something
	// else than SIGKILL. Or that epollCtl failed
	if p.isAlive() {
		err := syscall.Kill(int(p.pid), syscall.SIGKILL)
		if err != nil {
			return execution.UnknownStatusCode, errors.Wrap(err, "failed to kill process")
		}

		return uint32(128 + int(syscall.SIGKILL)), nil
	}

	data, err := ioutil.ReadFile(filepath.Join(p.stateDir, exitStatusFilename))
	if err != nil {
		return execution.UnknownStatusCode, errors.Wrap(err, "failed to read process exit status")
	}

	if len(data) == 0 {
		return execution.UnknownStatusCode, errors.New(execution.ErrProcessNotExited.Error())
	}

	status, err := strconv.Atoi(string(data))
	if err != nil {
		return execution.UnknownStatusCode, errors.Wrapf(err, "failed to parse exit status")
	}

	p.setStatus(execution.Stopped)
	return uint32(status), nil
}

func (p *process) Signal(sig os.Signal) error {
	err := syscall.Kill(int(p.pid), sig.(syscall.Signal))
	if err != nil {
		return errors.Wrap(err, "failed to signal process")
	}
	return nil
}

func (p *process) Status() execution.Status {
	p.mu.Lock()
	s := p.status
	p.mu.Unlock()
	return s
}

func (p *process) setStatus(s execution.Status) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()
}

func (p *process) isAlive() bool {
	if err := syscall.Kill(int(p.pid), 0); err != nil {
		if err == syscall.ESRCH {
			return false
		}
		log.G(p.ctx).WithFields(logrus.Fields{"process-id": p.ID(), "pid": p.pid}).
			Warnf("kill(0) failed: %v", err)
		return false
	}

	// check that we have the same starttime
	stime, err := starttime.GetProcessStartTime(int(p.pid))
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		log.G(p.ctx).WithFields(logrus.Fields{"process-id": p.ID(), "pid": p.pid}).
			Warnf("failed to get process start time: %v", err)
		return false
	}

	if p.startTime != stime {
		return false
	}

	return true
}

func (p *process) cleanup() {
	for _, f := range []*os.File{p.exitPipe, p.controlPipe} {
		if f != nil {
			f.Close()
		}
	}

	if err := os.RemoveAll(p.stateDir); err != nil {
		log.G(p.ctx).Warnf("failed to remove process state dir: %v", err)
	}
}

func waitUntilReady(ctx context.Context, abortCh chan syscall.WaitStatus, root string) (pid int64, stime string, status execution.Status, err error) {
	status = execution.Unknown
	for {
		select {
		case <-ctx.Done():
			return
		case wait := <-abortCh:
			if wait.Signaled() {
				err = errors.Errorf("shim died prematurely: %v", wait.Signal())
				return
			}
			err = errors.Errorf("shim exited prematurely with exit code %v", wait.ExitStatus())
			return
		default:
		}
		pid, err = getPidFromFile(filepath.Join(root, pidFilename))
		if err == nil {
			break
		} else if !os.IsNotExist(err) {
			return
		}
	}
	status = execution.Created
	stime, err = starttime.GetProcessStartTime(int(pid))
	switch {
	case os.IsNotExist(err):
		status = execution.Stopped
	case err != nil:
		return
	default:
		var b []byte
		path := filepath.Join(root, startTimeFilename)
		b, err = ioutil.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			err = ioutil.WriteFile(path, []byte(stime), 0600)
			if err != nil {
				return
			}
		case err != nil:
			err = errors.Wrapf(err, "failed to get start time for pid %d", pid)
			return
		case string(b) != stime:
			status = execution.Stopped
		}
	}

	return pid, stime, status, nil
}

func newShimProcess(o newProcessOpts) (*exec.Cmd, error) {
	cmd := exec.Command(o.shimBinary, o.container.ID(), o.container.Bundle(), o.runtime)
	cmd.Dir = o.stateDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	state := processState{
		Process:        o.Spec,
		Exec:           o.exec,
		Stdin:          o.Stdin,
		Stdout:         o.Stdout,
		Stderr:         o.Stderr,
		RuntimeArgs:    o.runtimeArgs,
		NoPivotRoot:    false,
		CheckpointPath: "",
		RootUID:        int(o.Spec.User.UID),
		RootGID:        int(o.Spec.User.GID),
	}

	f, err := os.Create(filepath.Join(o.stateDir, "process.json"))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create shim's process.json for container %s", o.container.ID())
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(state); err != nil {
		return nil, errors.Wrapf(err, "failed to create shim's processState for container %s", o.container.ID())
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrapf(err, "failed to start shim for container %s", o.container.ID())
	}

	return cmd, nil
}

func getControlPipes(root string) (exitPipe *os.File, controlPipe *os.File, err error) {
	path := filepath.Join(root, exitPipeFilename)
	if err = unix.Mkfifo(path, 0700); err != nil {
		err = errors.Wrap(err, "failed to create shim exit fifo")
		return
	}
	if exitPipe, err = os.OpenFile(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0); err != nil {
		err = errors.Wrap(err, "failed to open shim exit fifo")
		return
	}

	path = filepath.Join(root, controlPipeFilename)
	if err = unix.Mkfifo(path, 0700); err != nil {
		err = errors.Wrap(err, "failed to create shim control fifo")
		return
	}
	if controlPipe, err = os.OpenFile(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0); err != nil {
		err = errors.Wrap(err, "failed to open shim control fifo")
		return
	}

	return
}

func getPidFromFile(path string) (int64, error) {
	pid, err := runc.ReadPidFile(path)
	if err != nil {
		return -1, err
	}
	return int64(pid), nil
}

func getStartTimeFromFile(path string) (string, error) {
	stime, err := ioutil.ReadFile(path)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read start time")
	}
	return string(stime), nil
}

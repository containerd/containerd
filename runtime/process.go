package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/docker/containerd/specs"
	"github.com/docker/containerd/subreaper/exec"
	"golang.org/x/sys/unix"
)

type Process interface {
	io.Closer

	// ID of the process.
	// This is either "init" when it is the container's init process or
	// it is a user provided id for the process similar to the container id
	ID() string
	CloseStdin() error
	Resize(int, int) error
	// ExitFD returns the fd the provides an event when the process exits
	ExitFD() int
	// ExitStatus returns the exit status of the process or an error if it
	// has not exited
	ExitStatus() (int, error)
	// Spec returns the process spec that created the process
	Spec() specs.ProcessSpec
	// Signal sends the provided signal to the process
	Signal(os.Signal) error
	// Container returns the container that the process belongs to
	Container() Container
	// Stdio of the container
	Stdio() Stdio
	// SystemPid is the pid on the system
	SystemPid() int
	// State returns if the process is running or not
	State() State
	// Start executes the process
	Start() error
}

type processConfig struct {
	id          string
	root        string
	processSpec specs.ProcessSpec
	spec        *specs.Spec
	c           *container
	stdio       Stdio
	exec        bool
	checkpoint  string
}

func newProcess(config *processConfig) (*process, error) {
	p := &process{
		root:      config.root,
		id:        config.id,
		container: config.c,
		spec:      config.processSpec,
		stdio:     config.stdio,
	}
	uid, gid, err := getRootIDs(config.spec)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(config.root, "process.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ps := ProcessState{
		ProcessSpec: config.processSpec,
		Exec:        config.exec,
		PlatformProcessState: PlatformProcessState{
			Checkpoint: config.checkpoint,
			RootUID:    uid,
			RootGID:    gid,
		},
		Stdin:       config.stdio.Stdin,
		Stdout:      config.stdio.Stdout,
		Stderr:      config.stdio.Stderr,
		RuntimeArgs: config.c.runtimeArgs,
		NoPivotRoot: config.c.noPivotRoot,
	}

	if err := json.NewEncoder(f).Encode(ps); err != nil {
		return nil, err
	}
	exit, err := getExitPipe(filepath.Join(config.root, ExitFile))
	if err != nil {
		return nil, err
	}
	control, err := getControlPipe(filepath.Join(config.root, ControlFile))
	if err != nil {
		return nil, err
	}
	p.exitPipe = exit
	p.controlPipe = control
	return p, nil
}

func (p *process) Start() error {
	cmd := exec.Command(p.container.shim,
		p.container.id, p.container.bundle, p.container.runtime,
	)
	cmd.Dir = p.root
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	return p.startCmd(cmd)
}

func loadProcess(root, id string, c *container, s *ProcessState) (*process, error) {
	p := &process{
		root:      root,
		id:        id,
		container: c,
		spec:      s.ProcessSpec,
		stdio: Stdio{
			Stdin:  s.Stdin,
			Stdout: s.Stdout,
			Stderr: s.Stderr,
		},
	}
	if _, err := p.getPidFromFile(); err != nil {
		return nil, err
	}
	if _, err := p.ExitStatus(); err != nil {
		if err == ErrProcessNotExited {
			exit, err := getExitPipe(filepath.Join(root, ExitFile))
			if err != nil {
				return nil, err
			}
			p.exitPipe = exit
			return p, nil
		}
		return nil, err
	}
	return p, nil
}

type process struct {
	root        string
	id          string
	pid         int
	exitPipe    *os.File
	controlPipe *os.File
	container   *container
	spec        specs.ProcessSpec
	stdio       Stdio
}

func (p *process) ID() string {
	return p.id
}

func (p *process) Container() Container {
	return p.container
}

func (p *process) SystemPid() int {
	return p.pid
}

// ExitFD returns the fd of the exit pipe
func (p *process) ExitFD() int {
	return int(p.exitPipe.Fd())
}

func (p *process) CloseStdin() error {
	_, err := fmt.Fprintf(p.controlPipe, "%d %d %d\n", 0, 0, 0)
	return err
}

func (p *process) Resize(w, h int) error {
	_, err := fmt.Fprintf(p.controlPipe, "%d %d %d\n", 1, w, h)
	return err
}

func (p *process) ExitStatus() (int, error) {
	data, err := ioutil.ReadFile(filepath.Join(p.root, ExitStatusFile))
	if err != nil {
		if os.IsNotExist(err) {
			return -1, ErrProcessNotExited
		}
		return -1, err
	}
	if len(data) == 0 {
		return -1, ErrProcessNotExited
	}
	return strconv.Atoi(string(data))
}

func (p *process) Spec() specs.ProcessSpec {
	return p.spec
}

func (p *process) Stdio() Stdio {
	return p.stdio
}

// Close closes any open files and/or resouces on the process
func (p *process) Close() error {
	return p.exitPipe.Close()
}

func (p *process) State() State {
	if p.pid == 0 {
		return Stopped
	}
	err := syscall.Kill(p.pid, 0)
	if err != nil && err == syscall.ESRCH {
		return Stopped
	}
	return Running
}

func (p *process) getPidFromFile() (int, error) {
	data, err := ioutil.ReadFile(filepath.Join(p.root, "pid"))
	if err != nil {
		return -1, err
	}
	i, err := strconv.Atoi(string(data))
	if err != nil {
		return -1, errInvalidPidInt
	}
	p.pid = i
	return i, nil
}

func getExitPipe(path string) (*os.File, error) {
	if err := unix.Mkfifo(path, 0755); err != nil && !os.IsExist(err) {
		return nil, err
	}
	// add NONBLOCK in case the other side has already closed or else
	// this function would never return
	return os.OpenFile(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func getControlPipe(path string) (*os.File, error) {
	if err := unix.Mkfifo(path, 0755); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return os.OpenFile(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
}

// Signal sends the provided signal to the process
func (p *process) Signal(s os.Signal) error {
	return syscall.Kill(p.pid, s.(syscall.Signal))
}

func (p *process) startCmd(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		if exErr, ok := err.(*exec.Error); ok {
			if exErr.Err == exec.ErrNotFound || exErr.Err == os.ErrNotExist {
				return fmt.Errorf("%s not installed on system", p.container.shim)
			}
		}
		return err
	}
	if err := p.waitForStart(cmd); err != nil {
		return err
	}
	return nil
}

func (p *process) waitForStart(cmd *exec.Cmd) error {
	wc := make(chan error, 1)
	go func() {
		for {
			if _, err := p.getPidFromFile(); err != nil {
				if os.IsNotExist(err) || err == errInvalidPidInt {
					alive, err := isAlive(cmd)
					if err != nil {
						wc <- err
						return
					}
					if !alive {
						// runc could have failed to run the container so lets get the error
						// out of the logs or the shim could have encountered an error
						messages, err := readLogMessages(filepath.Join(p.root, "shim-log.json"))
						if err != nil && !os.IsNotExist(err) {
							wc <- err
							return
						}
						for _, m := range messages {
							if m.Level == "error" {
								wc <- fmt.Errorf("shim error: %v", m.Msg)
								return
							}
						}
						// no errors reported back from shim, check for runc/runtime errors
						messages, err = readLogMessages(filepath.Join(p.root, "log.json"))
						if err != nil {
							if os.IsNotExist(err) {
								err = ErrContainerNotStarted
							}
							wc <- err
							return
						}
						for _, m := range messages {
							if m.Level == "error" {
								wc <- fmt.Errorf("oci runtime error: %v", m.Msg)
								return
							}
						}
						wc <- ErrContainerNotStarted
						return
					}
					time.Sleep(15 * time.Millisecond)
					continue
				}
				wc <- err
				return
			}
			// the pid file was read successfully
			wc <- nil
			return
		}
	}()
	select {
	case err := <-wc:
		if err != nil {
			return err
		}
		return nil
	case <-time.After(p.container.timeout):
		cmd.Process.Kill()
		cmd.Wait()
		return ErrContainerStartTimeout
	}
}

// +build !windows

package shim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	shimapi "github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
)

type initProcess struct {
	sync.WaitGroup

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate go routines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu sync.Mutex

	id      string
	bundle  string
	console console.Console
	io      runc.IO
	runc    *runc.Runc
	status  int
	exited  time.Time
	pid     int
	closers []io.Closer
	stdin   io.Closer

	stdinPath  string
	stdoutPath string
	stderrPath string
	terminal   bool
}

func newInitProcess(context context.Context, path, namespace string, r *shimapi.CreateRequest) (*initProcess, error) {
	for _, rm := range r.Rootfs {
		m := &mount.Mount{
			Type:    rm.Type,
			Source:  rm.Source,
			Options: rm.Options,
		}
		if err := m.Mount(filepath.Join(path, "rootfs")); err != nil {
			return nil, errors.Wrapf(err, "failed to mount rootfs component %v", m)
		}
	}
	runtime := &runc.Runc{
		Command:      r.Runtime,
		Log:          filepath.Join(path, "log.json"),
		LogFormat:    runc.JSON,
		PdeathSignal: syscall.SIGKILL,
		Root:         filepath.Join(RuncRoot, namespace),
	}
	p := &initProcess{
		id:         r.ID,
		bundle:     r.Bundle,
		runc:       runtime,
		stdinPath:  r.Stdin,
		stdoutPath: r.Stdout,
		stderrPath: r.Stderr,
		terminal:   r.Terminal,
	}
	var (
		err    error
		socket *runc.Socket
		io     runc.IO
	)
	if r.Terminal {
		if socket, err = runc.NewConsoleSocket(filepath.Join(path, "pty.sock")); err != nil {
			return nil, errors.Wrap(err, "failed to create runc console socket")
		}
		defer os.Remove(socket.Path())
	} else {
		// TODO: get uid/gid
		if io, err = runc.NewPipeIO(0, 0); err != nil {
			return nil, errors.Wrap(err, "failed to create runc io pipes")
		}
		p.io = io
	}
	pidFile := filepath.Join(path, "init.pid")
	if r.Checkpoint != "" {
		opts := &runc.RestoreOpts{
			CheckpointOpts: runc.CheckpointOpts{
				ImagePath:  r.Checkpoint,
				WorkDir:    filepath.Join(r.Bundle, "work"),
				ParentPath: r.ParentCheckpoint,
			},
			PidFile:     pidFile,
			IO:          io,
			NoPivot:     r.NoPivot,
			Detach:      true,
			NoSubreaper: true,
		}
		if _, err := p.runc.Restore(context, r.ID, r.Bundle, opts); err != nil {
			return nil, p.runcError(err, "runc restore failed")
		}
	} else {
		opts := &runc.CreateOpts{
			PidFile: pidFile,
			IO:      io,
			NoPivot: r.NoPivot,
		}
		if socket != nil {
			opts.ConsoleSocket = socket
		}
		if err := p.runc.Create(context, r.ID, r.Bundle, opts); err != nil {
			return nil, p.runcError(err, "runc create failed")
		}
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context, r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to open stdin fifo %s", r.Stdin)
		}
		p.stdin = sc
		p.closers = append(p.closers, sc)
	}
	var copyWaitGroup sync.WaitGroup
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, errors.Wrap(err, "failed to retrieve console master")
		}
		p.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup); err != nil {
			return nil, errors.Wrap(err, "failed to start console copy")
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup); err != nil {
			return nil, errors.Wrap(err, "failed to start io pipe copy")
		}
	}

	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(pidFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve runc container pid")
	}
	p.pid = pid
	return p, nil
}

func (p *initProcess) Pid() int {
	return p.pid
}

func (p *initProcess) Status() int {
	return p.status
}

func (p *initProcess) ExitedAt() time.Time {
	return p.exited
}

// ContainerStatus return the state of the container (created, running, paused, stopped)
func (p *initProcess) ContainerStatus(ctx context.Context) (string, error) {
	c, err := p.runc.State(ctx, p.id)
	if err != nil {
		return "", p.runcError(err, "runc state failed")
	}
	return c.Status, nil
}

func (p *initProcess) Start(context context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.runc.Start(context, p.id)
	return p.runcError(err, "runc start failed")
}

func (p *initProcess) Exited(status int) {
	p.mu.Lock()
	p.status = status
	p.exited = time.Now()
	p.mu.Unlock()
}

func (p *initProcess) Delete(context context.Context) error {
	status, err := p.ContainerStatus(context)
	if err != nil {
		return err
	}
	if status != "stopped" {
		return fmt.Errorf("cannot delete a running container")
	}
	p.killAll(context)
	p.Wait()
	err = p.runc.Delete(context, p.id)
	if p.io != nil {
		for _, c := range p.closers {
			c.Close()
		}
		p.io.Close()
	}
	return p.runcError(err, "runc delete failed")
}

func (p *initProcess) Resize(ws console.WinSize) error {
	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

func (p *initProcess) Pause(context context.Context) error {
	err := p.runc.Pause(context, p.id)
	return p.runcError(err, "runc pause failed")
}

func (p *initProcess) Resume(context context.Context) error {
	err := p.runc.Resume(context, p.id)
	return p.runcError(err, "runc resume failed")
}

func (p *initProcess) Kill(context context.Context, signal uint32, all bool) error {
	err := p.runc.Kill(context, p.id, int(signal), &runc.KillOpts{
		All: all,
	})
	return p.runcError(err, "runc kill failed")
}

func (p *initProcess) killAll(context context.Context) error {
	err := p.runc.Kill(context, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
	return p.runcError(err, "runc killall failed")
}

func (p *initProcess) Signal(sig int) error {
	return unix.Kill(p.pid, syscall.Signal(sig))
}

func (p *initProcess) Stdin() io.Closer {
	return p.stdin
}

func (p *initProcess) Checkpoint(context context.Context, r *shimapi.CheckpointRequest) error {
	var actions []runc.CheckpointAction
	if !r.Exit {
		actions = append(actions, runc.LeaveRunning)
	}
	work := filepath.Join(p.bundle, "work")
	defer os.RemoveAll(work)
	if err := p.runc.Checkpoint(context, p.id, &runc.CheckpointOpts{
		WorkDir:                  work,
		ImagePath:                r.CheckpointPath,
		AllowOpenTCP:             r.AllowTcp,
		AllowExternalUnixSockets: r.AllowUnixSockets,
		AllowTerminal:            r.AllowTerminal,
		FileLocks:                r.FileLocks,
		EmptyNamespaces:          r.EmptyNamespaces,
	}, actions...); err != nil {
		dumpLog := filepath.Join(p.bundle, "criu-dump.log")
		if cerr := copyFile(dumpLog, filepath.Join(work, "dump.log")); cerr != nil {
			log.G(context).Error(err)
		}
		return fmt.Errorf("%s path= %s", criuError(err), dumpLog)
	}
	return nil
}

// TODO(mlaventure): move to runc package?
func getLastRuncError(r *runc.Runc) (string, error) {
	if r.Log == "" {
		return "", nil
	}

	f, err := os.OpenFile(r.Log, os.O_RDONLY, 0400)
	if err != nil {
		return "", err
	}

	var (
		errMsg string
		log    struct {
			Level string
			Msg   string
			Time  time.Time
		}
	)

	dec := json.NewDecoder(f)
	for err = nil; err == nil; {
		if err = dec.Decode(&log); err != nil && err != io.EOF {
			return "", err
		}
		if log.Level == "error" {
			errMsg = strings.TrimSpace(log.Msg)
		}
	}

	return errMsg, nil
}

func (p *initProcess) runcError(rErr error, msg string) error {
	if rErr == nil {
		return nil
	}

	rMsg, err := getLastRuncError(p.runc)
	switch {
	case err != nil:
		return errors.Wrapf(err, "%s: %s (%s)", msg, "unable to retrieve runc error", err.Error())
	case rMsg == "":
		return errors.Wrap(err, msg)
	default:
		return errors.Errorf("%s: %s", msg, rMsg)
	}
}

// criuError returns only the first line of the error message from criu
// it tries to add an invalid dump log location when returning the message
func criuError(err error) string {
	parts := strings.Split(err.Error(), "\n")
	return parts[0]
}

func copyFile(to, from string) error {
	ff, err := os.Open(from)
	if err != nil {
		return err
	}
	defer ff.Close()
	tt, err := os.Create(to)
	if err != nil {
		return err
	}
	defer tt.Close()
	_, err = io.Copy(tt, ff)
	return err
}

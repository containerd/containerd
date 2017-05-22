// +build !windows

package shim

import (
	"context"
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
)

type initProcess struct {
	sync.WaitGroup

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate go routines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu      sync.Mutex
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
}

func newInitProcess(context context.Context, path string, r *shimapi.CreateRequest) (*initProcess, error) {
	for _, rm := range r.Rootfs {
		m := &mount.Mount{
			Type:    rm.Type,
			Source:  rm.Source,
			Options: rm.Options,
		}
		if err := m.Mount(filepath.Join(path, "rootfs")); err != nil {
			return nil, err
		}
	}
	runtime := &runc.Runc{
		Command:      r.Runtime,
		Log:          filepath.Join(path, "log.json"),
		LogFormat:    runc.JSON,
		PdeathSignal: syscall.SIGKILL,
	}
	p := &initProcess{
		id:     r.ID,
		bundle: r.Bundle,
		runc:   runtime,
	}
	var (
		err    error
		socket *runc.Socket
		io     runc.IO
	)
	if r.Terminal {
		if socket, err = runc.NewConsoleSocket(filepath.Join(path, "pty.sock")); err != nil {
			return nil, err
		}
		defer os.Remove(socket.Path())
	} else {
		// TODO: get uid/gid
		if io, err = runc.NewPipeIO(0, 0); err != nil {
			return nil, err
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
			return nil, err
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
			return nil, err
		}
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context, r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		p.stdin = sc
		p.closers = append(p.closers, sc)
	}
	var copyWaitGroup sync.WaitGroup
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, err
		}
		p.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup); err != nil {
			return nil, err
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup); err != nil {
			return nil, err
		}
	}
	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(pidFile)
	if err != nil {
		return nil, err
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
		return "", err
	}
	return c.Status, nil
}

func (p *initProcess) Start(context context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runc.Start(context, p.id)
}

func (p *initProcess) Exited(status int) {
	p.mu.Lock()
	p.status = status
	p.exited = time.Now()
	p.mu.Unlock()
}

func (p *initProcess) Delete(context context.Context) error {
	p.killAll(context)
	p.Wait()
	err := p.runc.Delete(context, p.id)
	if p.io != nil {
		for _, c := range p.closers {
			c.Close()
		}
		p.io.Close()
	}
	return err
}

func (p *initProcess) Resize(ws console.WinSize) error {
	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

func (p *initProcess) Pause(context context.Context) error {
	return p.runc.Pause(context, p.id)
}

func (p *initProcess) Resume(context context.Context) error {
	return p.runc.Resume(context, p.id)
}

func (p *initProcess) Kill(context context.Context, signal uint32, all bool) error {
	return p.runc.Kill(context, p.id, int(signal), &runc.KillOpts{
		All: all,
	})
}

func (p *initProcess) killAll(context context.Context) error {
	return p.runc.Kill(context, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
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
		ImagePath:                r.Image,
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

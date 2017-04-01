package shim

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/crosbymichael/console"
	runc "github.com/crosbymichael/go-runc"
	"github.com/docker/containerd"
	shimapi "github.com/docker/containerd/api/services/shim"
	"github.com/pkg/errors"
)

type initProcess struct {
	sync.WaitGroup

	id      string
	bundle  string
	console console.Console
	io      runc.IO
	runc    *runc.Runc
	status  int
	pid     int
}

func newInitProcess(context context.Context, path string, r *shimapi.CreateRequest) (*initProcess, error) {
	for _, rm := range r.Rootfs {
		m := &containerd.Mount{
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
		socket *runc.ConsoleSocket
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
	opts := &runc.CreateOpts{
		PidFile:       filepath.Join(path, "init.pid"),
		ConsoleSocket: socket,
		IO:            io,
		NoPivot:       r.NoPivot,
	}
	sigchld := make(chan os.Signal, 2048)
	// main.go calls Notify as well, but it does not hurt
	signal.Notify(sigchld, syscall.SIGCHLD)
	if err := p.runc.Create(context, r.ID, r.Bundle, opts); err != nil {
		return nil, err
	}
	if socket != nil {
		console, err := receiveConsoleSocketMaster(socket, sigchld, 5*time.Second)
		if err != nil {
			return nil, err
		}
		p.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup); err != nil {
			return nil, err
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup); err != nil {
			return nil, err
		}
	}
	signal.Stop(sigchld)
	pid, err := runc.ReadPidFile(opts.PidFile)
	if err != nil {
		return nil, err
	}
	p.pid = pid
	return p, nil
}

func receiveConsoleSocketMaster(socket *runc.ConsoleSocket, sigchld chan os.Signal, deadline time.Duration) (console.Console, error) {
	var cons console.Console
	var err error
	done := make(chan struct{}, 1)
	go func() {
		cons, err = socket.ReceiveMaster()
		done <- struct{}{}
	}()
	select {
	case s := <-sigchld:
		err = errors.Errorf("got signal while receiving pty master: %v", s)
	case <-time.After(deadline):
		err = errors.Errorf("exceeded deadline %v while receiving pty master", deadline)
	case <-done:
	}
	return cons, err
}

func (p *initProcess) Pid() int {
	return p.pid
}

func (p *initProcess) Status() int {
	return p.status
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
	return p.runc.Start(context, p.id)
}

func (p *initProcess) Exited(status int) {
	p.status = status
}

func (p *initProcess) Delete(context context.Context) error {
	p.killAll(context)
	p.Wait()
	err := p.runc.Delete(context, p.id)
	if p.io != nil {
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

func (p *initProcess) killAll(context context.Context) error {
	return p.runc.Kill(context, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
}

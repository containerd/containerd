package shim

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/crosbymichael/console"
	runc "github.com/crosbymichael/go-runc"
	"github.com/docker/containerd"
	shimapi "github.com/docker/containerd/api/services/shim"
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

func newInitProcess(context context.Context, r *shimapi.CreateRequest) (*initProcess, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for _, rm := range r.Rootfs {
		m := &containerd.Mount{
			Type:    rm.Type,
			Source:  rm.Source,
			Options: rm.Options,
		}
		if err := m.Mount(filepath.Join(cwd, "rootfs")); err != nil {
			return nil, err
		}
	}
	runtime := &runc.Runc{
		Command:      r.Runtime,
		Log:          filepath.Join(cwd, "log.json"),
		LogFormat:    runc.JSON,
		PdeathSignal: syscall.SIGKILL,
	}
	p := &initProcess{
		id:     r.ID,
		bundle: r.Bundle,
		runc:   runtime,
	}
	var (
		socket *runc.ConsoleSocket
		io     runc.IO
	)
	if r.Terminal {
		if socket, err = runc.NewConsoleSocket(filepath.Join(cwd, "pty.sock")); err != nil {
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
		PidFile:       filepath.Join(cwd, "init.pid"),
		ConsoleSocket: socket,
		IO:            io,
		NoPivot:       r.NoPivot,
	}
	if err := p.runc.Create(context, r.ID, r.Bundle, opts); err != nil {
		return nil, err
	}
	if socket != nil {
		console, err := socket.ReceiveMaster()
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
	pid, err := runc.ReadPidFile(opts.PidFile)
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

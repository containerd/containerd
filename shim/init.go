package shim

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	runc "github.com/crosbymichael/go-runc"
	apishim "github.com/docker/containerd/api/shim"
)

type initProcess struct {
	sync.WaitGroup

	id      string
	bundle  string
	console *runc.Console
	io      runc.IO
	runc    *runc.Runc
	status  int
	pid     int
}

func newInitProcess(context context.Context, r *apishim.CreateRequest) (*initProcess, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
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
	p.io.Close()
	return err
}

func (p *initProcess) Resize(ws runc.WinSize) error {
	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

func (p *initProcess) killAll(context context.Context) error {
	return p.runc.Kill(context, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
}

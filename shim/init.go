package shim

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/crosbymichael/console"
	runc "github.com/crosbymichael/go-runc"
	apishim "github.com/docker/containerd/api/shim"
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

func logPath(format runc.Format) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	switch format {
	case runc.JSON:
		return filepath.Join(cwd, "log.json"), nil
	}
	return "", fmt.Errorf("%s is not supported", format)
}

func newInitProcess(context context.Context, r *apishim.CreateRequest) (*initProcess, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	logJSONPath, err := logPath(runc.JSON)
	if err != nil {
		return nil, err
	}
	runtime := &runc.Runc{
		Command:      r.Runtime,
		Log:          logJSONPath,
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

// readInitProcessLog reads the log.
// this function doesn't have a *initProcess receiver so that it works even
// when *initProcess is nil
func readInitProcessLog() ([]byte, runc.Format, error) {
	logJSONPath, err := logPath(runc.JSON)
	if err != nil {
		return nil, runc.JSON, err
	}
	logBytes, err := ioutil.ReadFile(logJSONPath)
	return logBytes, runc.JSON, err
}

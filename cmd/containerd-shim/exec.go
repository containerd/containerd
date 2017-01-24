package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	runc "github.com/crosbymichael/go-runc"
	"github.com/docker/containerd/api/shim"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type execProcess struct {
	sync.WaitGroup

	id      string
	console *runc.Console
	io      runc.IO
	status  int
	pid     int

	parent *initProcess
}

func newExecProcess(context context.Context, r *shim.ExecRequest, parent *initProcess) (process, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	e := &execProcess{
		id:     r.ID,
		parent: parent,
	}
	var (
		socket  *runc.ConsoleSocket
		io      runc.IO
		pidfile = filepath.Join(cwd, fmt.Sprintf("%s.pid", r.ID))
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
		e.io = io
	}
	opts := &runc.ExecOpts{
		PidFile:       pidfile,
		ConsoleSocket: socket,
		IO:            io,
		Detach:        true,
		Tty:           socket != nil,
	}
	if err := parent.runc.Exec(context, r.ID, processFromRequest(r), opts); err != nil {
		return nil, err
	}
	pid, err := runc.ReadPidFile(opts.PidFile)
	if err != nil {
		return nil, err
	}
	e.pid = pid
	return e, nil
}

func processFromRequest(r *shim.ExecRequest) specs.Process {
	return specs.Process{
		Terminal: r.Terminal,
		User: specs.User{
			UID:            r.User.Uid,
			GID:            r.User.Gid,
			AdditionalGids: r.User.AdditionalGids,
		},
		Rlimits:         rlimits(r.Rlimits),
		Args:            r.Args,
		Env:             r.Env,
		Cwd:             r.Cwd,
		Capabilities:    r.Capabilities,
		NoNewPrivileges: r.NoNewPrivileges,
		ApparmorProfile: r.ApparmorProfile,
		SelinuxLabel:    r.SelinuxLabel,
	}
}

func rlimits(rr []*shim.Rlimit) (o []specs.LinuxRlimit) {
	for _, r := range rr {
		o = append(o, specs.LinuxRlimit{
			Type: r.Type,
			Hard: r.Hard,
			Soft: r.Soft,
		})
	}
	return o
}

func (e *execProcess) Pid() int {
	return e.pid
}

func (e *execProcess) Status() int {
	return e.status
}

func (e *execProcess) Exited(status int) {
	e.status = status
}

func (e *execProcess) Start(_ context.Context) error {
	return nil
}

func (e *execProcess) Delete(context context.Context) error {
	e.Wait()
	e.io.Close()
	return nil
}

func (e *execProcess) Resize(ws runc.WinSize) error {
	if e.console == nil {
		return nil
	}
	return e.console.Resize(ws)
}

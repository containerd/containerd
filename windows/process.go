// +build windows

package windows

import (
	"context"

	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/windows/hcs"
)

// process implements containerd.Process and containerd.State
type process struct {
	*hcs.Process
}

func (p *process) State(ctx context.Context) (runtime.State, error) {
	return runtime.State{
		Pid:    p.Pid(),
		Status: p.Status(),
	}, nil
}

func (p *process) Kill(ctx context.Context, sig uint32, all bool) error {
	return p.Process.Kill()
}

func (p *process) Status() runtime.Status {
	return p.Process.Status()
}

func (p *process) Pid() uint32 {
	return p.Process.Pid()
}

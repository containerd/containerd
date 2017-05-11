// +build windows

package windows

import (
	"context"

	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/windows/hcs"
)

// process implements containerd.Process and containerd.State
type process struct {
	*hcs.Process
}

func (p *process) State(ctx context.Context) (plugin.State, error) {
	return p, nil
}

func (p *process) Kill(ctx context.Context, sig uint32, all bool) error {
	return p.Process.Kill()
}

func (p *process) Status() plugin.Status {
	return p.Process.Status()
}

func (p *process) Pid() uint32 {
	return p.Process.Pid()
}

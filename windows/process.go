// +build windows

package windows

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/windows/hcs"
	"golang.org/x/net/context"
)

// process implements containerd.Process and containerd.State
type process struct {
	*hcs.Process
}

func (p *process) State(ctx context.Context) (containerd.State, error) {
	return p, nil
}

func (p *process) Kill(ctx context.Context, sig uint32, all bool) error {
	return p.Process.Kill()
}

func (p *process) Status() containerd.Status {
	return p.Process.Status()
}

func (p *process) Pid() uint32 {
	return p.Process.Pid()
}

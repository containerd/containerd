// +build windows

package windows

import (
	"encoding/json"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/windows/hcs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	winsys "golang.org/x/sys/windows"
)

var (
	ErrLoadedContainer = errors.New("loaded container can only be terminated")
)

type eventCallback func(id string, evType containerd.EventType, pid, exitStatus uint32)

func loadContainers(ctx context.Context, h *hcs.HCS, sendEvent eventCallback) ([]*container, error) {
	hCtr, err := h.LoadContainers(ctx)
	if err != nil {
		return nil, err
	}

	containers := make([]*container, 0)
	for _, c := range hCtr {
		containers = append(containers, &container{
			ctr:       c,
			status:    containerd.RunningStatus,
			sendEvent: sendEvent,
		})
	}

	return containers, nil
}

func newContainer(ctx context.Context, h *hcs.HCS, id string, spec RuntimeSpec, io containerd.IO, sendEvent eventCallback) (*container, error) {
	cio, err := hcs.NewIO(io.Stdin, io.Stdout, io.Stderr, io.Terminal)
	if err != nil {
		return nil, err
	}

	hcsCtr, err := h.CreateContainer(ctx, id, spec.OCISpec, spec.Configuration, cio)
	if err != nil {
		return nil, err
	}
	sendEvent(id, containerd.CreateEvent, hcsCtr.Pid(), 0)

	return &container{
		ctr:       hcsCtr,
		status:    containerd.CreatedStatus,
		sendEvent: sendEvent,
	}, nil
}

// container implements both containerd.Container and containerd.State
type container struct {
	sync.Mutex

	ctr       *hcs.Container
	status    containerd.Status
	sendEvent eventCallback
}

func (c *container) Info() containerd.ContainerInfo {
	return containerd.ContainerInfo{
		ID:      c.ctr.ID(),
		Runtime: runtimeName,
	}
}

func (c *container) Start(ctx context.Context) error {
	if c.ctr.Pid() == 0 {
		return ErrLoadedContainer
	}

	err := c.ctr.Start(ctx)
	if err != nil {
		return err
	}

	c.setStatus(containerd.RunningStatus)
	c.sendEvent(c.ctr.ID(), containerd.StartEvent, c.ctr.Pid(), 0)

	// Wait for our process to terminate
	go func() {
		ec, err := c.ctr.ExitCode()
		if err != nil {
			log.G(ctx).Debug(err)
		}
		c.setStatus(containerd.StoppedStatus)
		c.sendEvent(c.ctr.ID(), containerd.ExitEvent, c.ctr.Pid(), ec)
	}()

	return nil
}

func (c *container) State(ctx context.Context) (containerd.State, error) {
	return c, nil
}

func (c *container) Kill(ctx context.Context, signal uint32, all bool) error {
	if winsys.Signal(signal) == winsys.SIGKILL {
		return c.ctr.Kill(ctx)
	}
	return c.ctr.Stop(ctx)
}

func (c *container) Exec(ctx context.Context, opts containerd.ExecOpts) (containerd.Process, error) {
	if c.ctr.Pid() == 0 {
		return nil, ErrLoadedContainer
	}

	pio, err := hcs.NewIO(opts.IO.Stdin, opts.IO.Stdout, opts.IO.Stderr, opts.IO.Terminal)
	if err != nil {
		return nil, err
	}

	var procSpec specs.Process
	if err := json.Unmarshal(opts.Spec, &procSpec); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal oci spec")
	}

	p, err := c.ctr.AddProcess(ctx, procSpec, pio)
	if err != nil {
		return nil, err
	}

	go func() {
		ec, err := p.ExitCode()
		if err != nil {
			log.G(ctx).Debug(err)
		}
		c.sendEvent(c.ctr.ID(), containerd.ExitEvent, p.Pid(), ec)
	}()

	return &process{p}, nil
}

func (c *container) CloseStdin(ctx context.Context, pid uint32) error {
	return c.ctr.CloseStdin(ctx, pid)
}

func (c *container) Pty(ctx context.Context, pid uint32, size containerd.ConsoleSize) error {
	return c.ctr.Pty(ctx, pid, size)
}

func (c *container) Status() containerd.Status {
	return c.getStatus()
}

func (c *container) Pid() uint32 {
	return c.ctr.Pid()
}

func (c *container) setStatus(status containerd.Status) {
	c.Lock()
	c.status = status
	c.Unlock()
}

func (c *container) getStatus() containerd.Status {
	c.Lock()
	defer c.Unlock()
	return c.status
}

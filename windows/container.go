// +build windows

package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/windows/hcs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	winsys "golang.org/x/sys/windows"
)

var (
	ErrLoadedContainer = errors.New("loaded container can only be terminated")
)

type eventCallback func(id string, evType plugin.EventType, pid, exitStatus uint32, exitedAt time.Time)

func loadContainers(ctx context.Context, h *hcs.HCS, sendEvent eventCallback) ([]*container, error) {
	hCtr, err := h.LoadContainers(ctx)
	if err != nil {
		return nil, err
	}

	containers := make([]*container, 0)
	for _, c := range hCtr {
		containers = append(containers, &container{
			ctr:       c,
			status:    plugin.RunningStatus,
			sendEvent: sendEvent,
		})
	}

	return containers, nil
}

func newContainer(ctx context.Context, h *hcs.HCS, id string, spec RuntimeSpec, io plugin.IO, sendEvent eventCallback) (*container, error) {
	cio, err := hcs.NewIO(io.Stdin, io.Stdout, io.Stderr, io.Terminal)
	if err != nil {
		return nil, err
	}

	hcsCtr, err := h.CreateContainer(ctx, id, spec.OCISpec, spec.Configuration, cio)
	if err != nil {
		return nil, err
	}
	sendEvent(id, plugin.CreateEvent, hcsCtr.Pid(), 0, time.Time{})

	return &container{
		ctr:       hcsCtr,
		status:    plugin.CreatedStatus,
		sendEvent: sendEvent,
	}, nil
}

type container struct {
	sync.Mutex

	ctr       *hcs.Container
	status    plugin.Status
	sendEvent eventCallback
}

func (c *container) Info() plugin.TaskInfo {
	return plugin.TaskInfo{
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

	c.setStatus(plugin.RunningStatus)
	c.sendEvent(c.ctr.ID(), plugin.StartEvent, c.ctr.Pid(), 0, time.Time{})

	// Wait for our process to terminate
	go func() {
		ec, err := c.ctr.ExitCode()
		if err != nil {
			log.G(ctx).Debug(err)
		}
		c.setStatus(plugin.StoppedStatus)
		c.sendEvent(c.ctr.ID(), plugin.ExitEvent, c.ctr.Pid(), ec, c.ctr.Processes()[0].ExitedAt())
	}()

	return nil
}

func (c *container) Pause(ctx context.Context) error {
	if c.ctr.GetConfiguration().UseHyperV == false {
		return fmt.Errorf("Windows non-HyperV containers do not support pause")
	}
	return c.ctr.Pause()
}

func (c *container) Resume(ctx context.Context) error {
	if c.ctr.GetConfiguration().UseHyperV == false {
		return fmt.Errorf("Windows non-HyperV containers do not support resume")
	}
	return c.ctr.Resume()
}

func (c *container) State(ctx context.Context) (plugin.State, error) {
	return c, nil
}

func (c *container) Kill(ctx context.Context, signal uint32, pid uint32, all bool) error {
	if winsys.Signal(signal) == winsys.SIGKILL {
		return c.ctr.Kill(ctx)
	}
	return c.ctr.Stop(ctx)
}

func (c *container) Exec(ctx context.Context, opts plugin.ExecOpts) (plugin.Process, error) {
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
		c.sendEvent(c.ctr.ID(), plugin.ExitEvent, p.Pid(), ec, p.ExitedAt())
	}()

	return &process{p}, nil
}

func (c *container) CloseStdin(ctx context.Context, pid uint32) error {
	return c.ctr.CloseStdin(ctx, pid)
}

func (c *container) Pty(ctx context.Context, pid uint32, size plugin.ConsoleSize) error {
	return c.ctr.Pty(ctx, pid, size)
}

func (c *container) Status() plugin.Status {
	return c.getStatus()
}

func (c *container) Pid() uint32 {
	return c.ctr.Pid()
}

func (c *container) Processes(ctx context.Context) ([]uint32, error) {
	pl, err := c.ctr.ProcessList()
	if err != nil {
		return nil, err
	}

	pids := make([]uint32, 0, len(pl))
	for _, p := range pl {
		pids = append(pids, p.ProcessId)
	}

	return pids, nil
}

func (c *container) Checkpoint(ctx context.Context, opts plugin.CheckpointOpts) error {
	return fmt.Errorf("Windows containers do not support checkpoint")
}

func (c *container) setStatus(status plugin.Status) {
	c.Lock()
	c.status = status
	c.Unlock()
}

func (c *container) getStatus() plugin.Status {
	c.Lock()
	defer c.Unlock()
	return c.status
}

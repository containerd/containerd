// +build windows

package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/windows/hcs"
	"github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	winsys "golang.org/x/sys/windows"
)

var ErrLoadedContainer = errors.New("loaded container can only be terminated")

type eventCallback func(id string, evType events.RuntimeEvent_EventType, pid, exitStatus uint32, exitedAt time.Time)

func loadContainers(ctx context.Context, h *hcs.HCS, sendEvent eventCallback) ([]*container, error) {
	hCtr, err := h.LoadContainers(ctx)
	if err != nil {
		return nil, err
	}

	containers := make([]*container, 0)
	for _, c := range hCtr {
		containers = append(containers, &container{
			ctr:       c,
			status:    runtime.RunningStatus,
			sendEvent: sendEvent,
		})
	}

	return containers, nil
}

func newContainer(ctx context.Context, h *hcs.HCS, id string, spec *RuntimeSpec, io runtime.IO, sendEvent eventCallback) (*container, error) {
	cio, err := hcs.NewIO(io.Stdin, io.Stdout, io.Stderr, io.Terminal)
	if err != nil {
		return nil, err
	}

	hcsCtr, err := h.CreateContainer(ctx, id, spec.OCISpec, spec.Configuration, cio)
	if err != nil {
		return nil, err
	}
	sendEvent(id, events.RuntimeEvent_CREATE, hcsCtr.Pid(), 0, time.Time{})

	return &container{
		ctr:       hcsCtr,
		status:    runtime.CreatedStatus,
		sendEvent: sendEvent,
	}, nil
}

type container struct {
	sync.Mutex

	ctr       *hcs.Container
	status    runtime.Status
	sendEvent eventCallback
}

func (c *container) ID() string {
	return c.ctr.ID()
}

func (c *container) Info() runtime.TaskInfo {
	return runtime.TaskInfo{
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

	c.setStatus(runtime.RunningStatus)
	c.sendEvent(c.ctr.ID(), events.RuntimeEvent_START, c.ctr.Pid(), 0, time.Time{})

	// Wait for our process to terminate
	go func() {
		ec, err := c.ctr.ExitCode()
		if err != nil {
			log.G(ctx).Debug(err)
		}
		c.setStatus(runtime.StoppedStatus)
		c.sendEvent(c.ctr.ID(), events.RuntimeEvent_EXIT, c.ctr.Pid(), ec, c.ctr.Processes()[0].ExitedAt())
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

func (c *container) State(ctx context.Context) (runtime.State, error) {
	return runtime.State{
		Pid:    c.Pid(),
		Status: c.Status(),
	}, nil
}

func (c *container) Kill(ctx context.Context, signal uint32, all bool) error {
	if winsys.Signal(signal) == winsys.SIGKILL {
		return c.ctr.Kill(ctx)
	}
	return c.ctr.Stop(ctx)
}

func (c *container) Process(ctx context.Context, id string) (runtime.Process, error) {
	for _, p := range c.ctr.Processes() {
		if p.ID() == id {
			return &process{p}, nil
		}
	}
	return nil, errors.Errorf("process %s not found", id)
}

func (c *container) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (runtime.Process, error) {
	if c.ctr.Pid() == 0 {
		return nil, ErrLoadedContainer
	}

	pio, err := hcs.NewIO(opts.IO.Stdin, opts.IO.Stdout, opts.IO.Stderr, opts.IO.Terminal)
	if err != nil {
		return nil, err
	}

	var procSpec specs.Process
	if err := json.Unmarshal(opts.Spec.Value, &procSpec); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal oci spec")
	}

	p, err := c.ctr.AddProcess(ctx, id, &procSpec, pio)
	if err != nil {
		return nil, err
	}

	go func() {
		ec, err := p.ExitCode()
		if err != nil {
			log.G(ctx).Debug(err)
		}
		c.sendEvent(c.ctr.ID(), events.RuntimeEvent_EXEC_ADDED, p.Pid(), ec, p.ExitedAt())
	}()

	return &process{p}, nil
}

func (c *container) CloseIO(ctx context.Context) error {
	return c.ctr.CloseIO(ctx)
}

func (c *container) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
	return c.ctr.ResizePty(ctx, size)
}

func (c *container) Status() runtime.Status {
	return c.getStatus()
}

func (c *container) Pid() uint32 {
	return c.ctr.Pid()
}

func (c *container) Pids(ctx context.Context) ([]uint32, error) {
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

func (c *container) Checkpoint(ctx context.Context, _ string, _ *types.Any) error {
	return fmt.Errorf("Windows containers do not support checkpoint")
}

func (c *container) DeleteProcess(ctx context.Context, id string) (*runtime.Exit, error) {
	var process *hcs.Process
	for _, p := range c.ctr.Processes() {
		if p.ID() == id {
			process = p
			break
		}
	}
	if process == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}
	ec, err := process.ExitCode()
	if err != nil {
		return nil, err
	}
	process.Delete()
	return &runtime.Exit{
		Status:    ec,
		Timestamp: process.ExitedAt(),
	}, nil
}

func (c *container) Update(ctx context.Context, spec *types.Any) error {
	return fmt.Errorf("Windows containers do not support update")
}

func (c *container) setStatus(status runtime.Status) {
	c.Lock()
	c.status = status
	c.Unlock()
}

func (c *container) getStatus() runtime.Status {
	c.Lock()
	defer c.Unlock()
	return c.status
}

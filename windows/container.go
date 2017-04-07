// +build windows

package windows

import (
	"encoding/json"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/windows/hcs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var (
	ErrLoadedContainer = errors.New("loaded container can only be terminated")
)

type State struct {
	pid    uint32
	status containerd.Status
}

func (s State) Pid() uint32 {
	return s.pid
}

func (s State) Status() containerd.Status {
	return s.status
}

type eventCallback func(id string, evType containerd.EventType, pid, exitStatus uint32)

func loadContainers(ctx context.Context, rootDir string) ([]*container, error) {
	hcs, err := hcs.LoadAll(ctx, owner, rootDir)
	if err != nil {
		return nil, err
	}

	containers := make([]*container, 0)
	for id, h := range hcs {
		containers = append(containers, &container{
			id:     id,
			status: containerd.RunningStatus,
			hcs:    h,
		})
	}

	return containers, nil
}

func newContainer(id, rootDir string, pid uint32, spec RuntimeSpec, io containerd.IO, sendEvent eventCallback) (*container, error) {
	hcs, err := hcs.New(rootDir, owner, id, spec.OCISpec, spec.Configuration, io)
	if err != nil {
		return nil, err
	}

	return &container{
		runtimePid: pid,
		id:         id,
		hcs:        hcs,
		status:     containerd.CreatedStatus,
		ecSync:     make(chan struct{}),
		sendEvent:  sendEvent,
	}, nil
}

type container struct {
	sync.Mutex

	runtimePid uint32
	id         string
	hcs        *hcs.HCS
	status     containerd.Status

	ec        uint32
	ecErr     error
	ecSync    chan struct{}
	sendEvent func(id string, evType containerd.EventType, pid, exitStatus uint32)
}

func (c *container) Info() containerd.ContainerInfo {
	return containerd.ContainerInfo{
		ID:      c.id,
		Runtime: runtimeName,
	}
}

func (c *container) Start(ctx context.Context) error {
	if c.runtimePid == 0 {
		return ErrLoadedContainer
	}

	err := c.hcs.Start(ctx, false)
	if err != nil {
		c.hcs.Terminate(ctx)
		c.sendEvent(c.id, containerd.ExitEvent, c.runtimePid, 255)
		return err
	}

	c.setStatus(containerd.RunningStatus)
	c.sendEvent(c.id, containerd.StartEvent, c.runtimePid, 0)

	// Wait for our process to terminate
	go func() {
		c.ec, c.ecErr = c.hcs.ExitCode(context.Background())
		c.setStatus(containerd.StoppedStatus)
		c.sendEvent(c.id, containerd.ExitEvent, c.runtimePid, c.ec)
		close(c.ecSync)
	}()

	return nil
}

func (c *container) State(ctx context.Context) (containerd.State, error) {
	return &State{
		pid:    c.runtimePid,
		status: c.getStatus(),
	}, nil
}

func (c *container) Kill(ctx context.Context, signal uint32, all bool) error {
	return c.hcs.Terminate(ctx)
}

func (c *container) Exec(ctx context.Context, opts containerd.ExecOpts) (containerd.Process, error) {
	if c.runtimePid == 0 {
		return nil, ErrLoadedContainer
	}

	var procSpec specs.Process
	if err := json.Unmarshal(opts.Spec, &procSpec); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal oci spec")
	}

	p, err := c.hcs.Exec(ctx, procSpec, opts.IO)
	if err != nil {
		return nil, err
	}

	go func() {
		ec, _ := p.ExitCode()
		c.sendEvent(c.id, containerd.ExitEvent, p.Pid(), ec)
	}()

	return &process{p}, nil
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

func (c *container) exitCode(ctx context.Context) (uint32, error) {
	if c.runtimePid == 0 {
		return 255, ErrLoadedContainer
	}

	<-c.ecSync
	return c.ec, c.ecErr
}

func (c *container) remove(ctx context.Context) error {
	return c.hcs.Remove(ctx)
}

func (c *container) getRuntimePid() uint32 {
	return c.runtimePid
}

type process struct {
	p *hcs.Process
}

func (p *process) State(ctx context.Context) (containerd.State, error) {
	return &processState{p.p}, nil
}

func (p *process) Kill(ctx context.Context, sig uint32, all bool) error {
	return p.p.Kill()
}

type processState struct {
	p *hcs.Process
}

func (s *processState) Status() containerd.Status {
	return s.p.Status()
}

func (s *processState) Pid() uint32 {
	return s.p.Pid()
}

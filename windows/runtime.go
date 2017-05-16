// +build windows

package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/windows/hcs"
	"github.com/containerd/containerd/windows/pid"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const (
	runtimeName = "windows"
	owner       = "containerd"
)

var _ = (plugin.Runtime)(&Runtime{})

func init() {
	plugin.Register(runtimeName, &plugin.Registration{
		Type: plugin.RuntimePlugin,
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {

	rootDir := filepath.Join(ic.Root, runtimeName)
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", rootDir)
	}

	c, cancel := context.WithCancel(ic.Context)
	r := &Runtime{
		pidPool:       pid.NewPool(),
		containers:    make(map[string]*container),
		events:        make(chan *plugin.Event, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
		rootDir:       rootDir,
		hcs:           hcs.New(owner, rootDir),
	}

	// Terminate all previous container that we may have started. We don't
	// support restoring containers
	ctrs, err := loadContainers(ic.Context, r.hcs, r.sendEvent)
	if err != nil {
		return nil, err
	}

	for _, c := range ctrs {
		c.ctr.Delete(ic.Context)
		r.sendEvent(c.ctr.ID(), plugin.ExitEvent, c.ctr.Pid(), 255, time.Time{})
	}

	// Try to delete the old state dir and recreate it
	stateDir := filepath.Join(ic.State, runtimeName)
	if err := os.RemoveAll(stateDir); err != nil {
		log.G(c).WithError(err).Warnf("failed to cleanup old state directory at %s", stateDir)
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", stateDir)
	}
	r.stateDir = stateDir

	return r, nil
}

type Runtime struct {
	sync.Mutex

	rootDir  string
	stateDir string
	pidPool  *pid.Pool

	hcs *hcs.HCS

	containers map[string]*container

	events        chan *plugin.Event
	eventsContext context.Context
	eventsCancel  func()
}

type RuntimeSpec struct {
	// Spec is the OCI spec
	OCISpec specs.Spec

	// HCS specific options
	hcs.Configuration
}

func (r *Runtime) Create(ctx context.Context, id string, opts plugin.CreateOpts) (plugin.Task, error) {
	var rtSpec RuntimeSpec
	if err := json.Unmarshal(opts.Spec, &rtSpec); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal oci spec")
	}

	ctr, err := newContainer(ctx, r.hcs, id, rtSpec, opts.IO, r.sendEvent)
	if err != nil {
		return nil, err
	}

	r.Lock()
	r.containers[id] = ctr
	r.Unlock()

	return ctr, nil
}

func (r *Runtime) Delete(ctx context.Context, c plugin.Task) (*plugin.Exit, error) {
	wc, ok := c.(*container)
	if !ok {
		return nil, fmt.Errorf("container cannot be cast as *windows.container")
	}
	ec, err := wc.ctr.ExitCode()
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to retrieve exit code for container %s", wc.ctr.ID())
	}

	wc.ctr.Delete(ctx)

	r.Lock()
	delete(r.containers, wc.ctr.ID())
	r.Unlock()

	return &plugin.Exit{
		Status:    ec,
		Timestamp: wc.ctr.Processes()[0].ExitedAt(),
	}, nil
}

func (r *Runtime) Tasks(ctx context.Context) ([]plugin.Task, error) {
	r.Lock()
	defer r.Unlock()
	list := make([]plugin.Task, len(r.containers))
	for _, c := range r.containers {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			list = append(list, c)
		}
	}

	return list, nil
}

func (r *Runtime) Events(ctx context.Context) <-chan *plugin.Event {
	return r.events
}

func (r *Runtime) sendEvent(id string, evType plugin.EventType, pid, exitStatus uint32, exitedAt time.Time) {
	r.events <- &plugin.Event{
		Timestamp:  time.Now(),
		Runtime:    runtimeName,
		Type:       evType,
		Pid:        pid,
		ID:         id,
		ExitStatus: exitStatus,
		ExitedAt:   exitedAt,
	}
}

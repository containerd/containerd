// +build windows

package windows

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/windows/hcs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"

	"golang.org/x/net/context"
)

const (
	runtimeName    = "windows"
	owner          = "containerd"
	configFilename = "config.json"
)

// Win32 error codes that are used for various workarounds
// These really should be ALL_CAPS to match golangs syscall library and standard
// Win32 error conventions, but golint insists on CamelCase.
const (
	CoEClassstring     = syscall.Errno(0x800401F3) // Invalid class string
	ErrorNoNetwork     = syscall.Errno(1222)       // The network is not present or not started
	ErrorBadPathname   = syscall.Errno(161)        // The specified path is invalid
	ErrorInvalidObject = syscall.Errno(0x800710D8) // The object identifier does not represent a valid object
)

func init() {
	plugin.Register(runtimeName, &plugin.Registration{
		Type: plugin.RuntimePlugin,
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	c, cancel := context.WithCancel(ic.Context)

	rootDir := filepath.Join(ic.Root, runtimeName)
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", rootDir)
	}

	// Terminate all previous container that we may have started. We don't
	// support restoring containers

	ctrs, err := loadContainers(ic.Context, rootDir)
	if err != nil {
		return nil, err
	}

	for _, c := range ctrs {
		c.remove(ic.Context)
	}

	// Try to delete the old state dir and recreate it
	stateDir := filepath.Join(ic.State, runtimeName)
	if err := os.RemoveAll(stateDir); err != nil {
		log.G(c).WithError(err).Warnf("failed to cleanup old state directory at %s", stateDir)
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", stateDir)
	}

	return &Runtime{
		containers:    make(map[string]*container),
		containersPid: make(map[uint32]struct{}),
		events:        make(chan *containerd.Event, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
		stateDir:      stateDir,
		rootDir:       rootDir,
	}, nil
}

type Runtime struct {
	sync.Mutex

	rootDir  string
	stateDir string

	containers    map[string]*container
	containersPid map[uint32]struct{}
	currentPid    uint32

	events        chan *containerd.Event
	eventsContext context.Context
	eventsCancel  func()
}

type RuntimeSpec struct {
	// Spec is the OCI spec
	OCISpec specs.Spec

	// HCS specific options
	hcs.Configuration
}

func (r *Runtime) Create(ctx context.Context, id string, opts containerd.CreateOpts) (containerd.Container, error) {
	var rtSpec RuntimeSpec
	if err := json.Unmarshal(opts.Spec, &rtSpec); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal oci spec")
	}

	pid, err := r.getPid()
	if err != nil {
		return nil, err
	}

	ctr, err := newContainer(id, r.rootDir, pid, rtSpec, opts.IO, func(id string, evType containerd.EventType, pid, exitStatus uint32) {
		r.sendEvent(id, evType, pid, exitStatus)
	})
	if err != nil {
		r.putPid(pid)
		return nil, err
	}

	r.Lock()
	r.containers[id] = ctr
	r.containersPid[pid] = struct{}{}
	r.Unlock()

	r.sendEvent(id, containerd.CreateEvent, pid, 0)

	return ctr, nil
}

func (r *Runtime) Delete(ctx context.Context, c containerd.Container) (uint32, error) {
	wc, ok := c.(*container)
	if !ok {
		return 0, fmt.Errorf("container cannot be cast as *windows.container")
	}
	ec, err := wc.exitCode(ctx)
	if err != nil {
		ec = 255
		log.G(ctx).WithError(err).Errorf("failed to retrieve exit code for container %s", c.Info().ID)
	}

	if err = wc.remove(ctx); err == nil {
		r.Lock()
		delete(r.containers, c.Info().ID)
		r.Unlock()
	}

	r.putPid(wc.getRuntimePid())

	return ec, err
}

func (r *Runtime) Containers() ([]containerd.Container, error) {
	r.Lock()
	list := make([]containerd.Container, len(r.containers))
	for _, c := range r.containers {
		list = append(list, c)
	}
	r.Unlock()

	return list, nil
}

func (r *Runtime) Events(ctx context.Context) <-chan *containerd.Event {
	return r.events
}

func (r *Runtime) sendEvent(id string, evType containerd.EventType, pid, exitStatus uint32) {
	r.events <- &containerd.Event{
		Timestamp:  time.Now(),
		Runtime:    runtimeName,
		Type:       evType,
		Pid:        pid,
		ID:         id,
		ExitStatus: exitStatus,
	}
}

func (r *Runtime) getPid() (uint32, error) {
	r.Lock()
	defer r.Unlock()

	pid := r.currentPid + 1
	for pid != r.currentPid {
		// 0 is reserved and invalid
		if pid == 0 {
			pid = 1
		}
		if _, ok := r.containersPid[pid]; !ok {
			r.currentPid = pid
			return pid, nil
		}
		pid++
	}

	return 0, errors.New("pid pool exhausted")
}

func (r *Runtime) putPid(pid uint32) {
	r.Lock()
	delete(r.containersPid, pid)
	r.Unlock()
}

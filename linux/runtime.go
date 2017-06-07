// +build linux

package linux

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/api/types/task"
	shimb "github.com/containerd/containerd/linux/shim"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	runc "github.com/containerd/go-runc"

	"golang.org/x/sys/unix"
)

const (
	runtimeName    = "linux"
	configFilename = "config.json"
	defaultRuntime = "runc"
	defaultShim    = "containerd-shim"
)

func init() {
	plugin.Register(runtimeName, &plugin.Registration{
		Type: plugin.RuntimePlugin,
		Init: New,
		Config: &Config{
			Shim:    defaultShim,
			Runtime: defaultRuntime,
		},
	})
}

var _ = (plugin.Runtime)(&Runtime{})

type Config struct {
	// Shim is a path or name of binary implementing the Shim GRPC API
	Shim string `toml:"shim,omitempty"`
	// Runtime is a path or name of an OCI runtime used by the shim
	Runtime string `toml:"runtime,omitempty"`
	// NoShim calls runc directly from within the pkg
	NoShim bool `toml:"no_shim,omitempty"`
}

func New(ic *plugin.InitContext) (interface{}, error) {
	path := filepath.Join(ic.State, runtimeName)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	cfg := ic.Config.(*Config)
	c, cancel := context.WithCancel(ic.Context)
	r := &Runtime{
		root:          path,
		remote:        !cfg.NoShim,
		shim:          cfg.Shim,
		runtime:       cfg.Runtime,
		events:        make(chan *plugin.Event, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
		monitor:       ic.Monitor,
	}
	// set the events output for a monitor if it generates events
	ic.Monitor.Events(r.events)
	return r, nil
}

type Runtime struct {
	root    string
	shim    string
	runtime string
	remote  bool

	events        chan *plugin.Event
	eventsContext context.Context
	eventsCancel  func()
	monitor       plugin.TaskMonitor
}

func (r *Runtime) Create(ctx context.Context, id string, opts plugin.CreateOpts) (t plugin.Task, err error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	path, err := r.newBundle(namespace, id, opts.Spec)
	if err != nil {
		return nil, err
	}
	s, err := newShim(r.shim, path, namespace, r.remote)
	if err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	// Exit the shim on error
	defer func() {
		if err != nil {
			s.Exit(context.Background(), &shim.ExitRequest{})
		}
	}()
	if err = r.handleEvents(s); err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	sopts := &shim.CreateRequest{
		ID:         id,
		Bundle:     path,
		Runtime:    r.runtime,
		Stdin:      opts.IO.Stdin,
		Stdout:     opts.IO.Stdout,
		Stderr:     opts.IO.Stderr,
		Terminal:   opts.IO.Terminal,
		Checkpoint: opts.Checkpoint,
	}
	for _, m := range opts.Rootfs {
		sopts.Rootfs = append(sopts.Rootfs, &mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	if _, err = s.Create(ctx, sopts); err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	c := newTask(id, opts.Spec, s)
	// after the task is created, add it to the monitor
	if err = r.monitor.Monitor(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *Runtime) Delete(ctx context.Context, c plugin.Task) (*plugin.Exit, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	lc, ok := c.(*Task)
	if !ok {
		return nil, fmt.Errorf("container cannot be cast as *linux.Container")
	}
	// remove the container from the monitor
	if err := r.monitor.Stop(lc); err != nil {
		// TODO: log error here
		return nil, err
	}
	rsp, err := lc.shim.Delete(ctx, &shim.DeleteRequest{})
	if err != nil {
		return nil, err
	}
	lc.shim.Exit(ctx, &shim.ExitRequest{})
	return &plugin.Exit{
		Status:    rsp.ExitStatus,
		Timestamp: rsp.ExitedAt,
	}, r.deleteBundle(namespace, lc.containerID)
}

func (r *Runtime) Tasks(ctx context.Context) ([]plugin.Task, error) {
	dir, err := ioutil.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	var o []plugin.Task
	for _, fi := range dir {
		if !fi.IsDir() {
			continue
		}
		tasks, err := r.loadContainers(ctx, fi.Name())
		if err != nil {
			return nil, err
		}
		o = append(o, tasks...)
	}
	return o, nil
}

func (r *Runtime) loadContainers(ctx context.Context, ns string) ([]plugin.Task, error) {
	dir, err := ioutil.ReadDir(filepath.Join(r.root, ns))
	if err != nil {
		return nil, err
	}
	var o []plugin.Task
	for _, fi := range dir {
		if !fi.IsDir() {
			continue
		}
		id := fi.Name()
		// TODO: optimize this if it is call frequently to list all containers
		// i.e. dont' reconnect to the the shim's ever time
		c, err := r.loadContainer(ctx, filepath.Join(r.root, ns, id))
		if err != nil {
			log.G(ctx).WithError(err).Warnf("failed to load container %s/%s", ns, id)
			// if we fail to load the container, connect to the shim, make sure if the shim has
			// been killed and cleanup the resources still being held by the container
			r.killContainer(ctx, ns, id)
			continue
		}
		o = append(o, c)
	}
	return o, nil
}

func (r *Runtime) Events(ctx context.Context) <-chan *plugin.Event {
	return r.events
}

func (r *Runtime) handleEvents(s shim.ShimClient) error {
	events, err := s.Events(r.eventsContext, &shim.EventsRequest{})
	if err != nil {
		return err
	}
	go r.forward(events)
	return nil
}

func (r *Runtime) forward(events shim.Shim_EventsClient) {
	for {
		e, err := events.Recv()
		if err != nil {
			if !strings.HasSuffix(err.Error(), "transport is closing") {
				log.G(r.eventsContext).WithError(err).Error("get event from shim")
			}
			return
		}
		var et plugin.EventType
		switch e.Type {
		case task.Event_CREATE:
			et = plugin.CreateEvent
		case task.Event_EXEC_ADDED:
			et = plugin.ExecAddEvent
		case task.Event_EXIT:
			et = plugin.ExitEvent
		case task.Event_OOM:
			et = plugin.OOMEvent
		case task.Event_START:
			et = plugin.StartEvent
		}
		r.events <- &plugin.Event{
			Timestamp:  time.Now(),
			Runtime:    runtimeName,
			Type:       et,
			Pid:        e.Pid,
			ID:         e.ID,
			ExitStatus: e.ExitStatus,
			ExitedAt:   e.ExitedAt,
		}
	}
}

func (r *Runtime) newBundle(namespace, id string, spec []byte) (string, error) {
	path := filepath.Join(r.root, namespace)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	path = filepath.Join(path, id)
	if err := os.Mkdir(path, 0700); err != nil {
		return "", err
	}
	if err := os.Mkdir(filepath.Join(path, "rootfs"), 0700); err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(path, configFilename))
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = io.Copy(f, bytes.NewReader(spec))
	return path, err
}

func (r *Runtime) deleteBundle(namespace, id string) error {
	return os.RemoveAll(filepath.Join(r.root, namespace, id))
}

func (r *Runtime) loadContainer(ctx context.Context, path string) (*Task, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	id := filepath.Base(path)
	s, err := loadShim(path, namespace, r.remote)
	if err != nil {
		return nil, err
	}
	if err = r.handleEvents(s); err != nil {
		return nil, err
	}
	data, err := ioutil.ReadFile(filepath.Join(path, configFilename))
	if err != nil {
		return nil, err
	}
	return &Task{
		containerID: id,
		shim:        s,
		spec:        data,
	}, nil
}

// killContainer is used whenever the runtime fails to connect to a shim (it died)
// and needs to cleanup the container resources in the underlying runtime (runc, etc...)
func (r *Runtime) killContainer(ctx context.Context, ns, id string) {
	log.G(ctx).Debug("terminating container after failed load")
	runtime := &runc.Runc{
		// TODO: should we get Command provided for initial container creation?
		Command:      r.runtime,
		LogFormat:    runc.JSON,
		PdeathSignal: unix.SIGKILL,
		Root:         filepath.Join(shimb.RuncRoot, ns),
	}
	if err := runtime.Kill(ctx, id, int(unix.SIGKILL), &runc.KillOpts{
		All: true,
	}); err != nil {
		log.G(ctx).WithError(err).Warnf("kill all processes for %s", id)
	}
	// it can take a while for the container to be killed so poll for the container's status
	// until it is in a stopped state
	status := "running"
	for status != "stopped" {
		c, err := runtime.State(ctx, id)
		if err != nil {
			break
		}
		status = c.Status
		time.Sleep(10 * time.Millisecond)
	}
	if err := runtime.Delete(ctx, id); err != nil {
		log.G(ctx).WithError(err).Warnf("delete container %s", id)
	}
	// try to unmount the rootfs in case it was not owned by an external mount namespace
	unix.Unmount(filepath.Join(r.root, ns, id, "rootfs"), 0)
	// remove container bundle
	if err := r.deleteBundle(ns, id); err != nil {
		log.G(ctx).WithError(err).Warnf("delete container bundle %s", id)
	}
}

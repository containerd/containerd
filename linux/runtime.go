package linux

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/containerd"
	"github.com/docker/containerd/api/services/shim"
	"github.com/docker/containerd/api/types/container"
	"github.com/docker/containerd/api/types/mount"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/plugin"

	"golang.org/x/net/context"
)

const (
	runtimeName    = "linux"
	configFilename = "config.json"
	defaultRuntime = "runc"
)

func init() {
	plugin.Register(runtimeName, &plugin.Registration{
		Type:   plugin.RuntimePlugin,
		Init:   New,
		Config: &Config{},
	})
}

type Config struct {
	// Runtime is a path or name of an OCI runtime used by the shim
	Runtime string `toml:"runtime"`
}

func New(ic *plugin.InitContext) (interface{}, error) {
	path := filepath.Join(ic.State, runtimeName)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	cfg := ic.Config.(*Config)
	if cfg.Runtime == "" {
		cfg.Runtime = defaultRuntime
	}
	c, cancel := context.WithCancel(ic.Context)
	return &Runtime{
		root:          path,
		runtime:       cfg.Runtime,
		events:        make(chan *containerd.Event, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
	}, nil
}

type Runtime struct {
	root    string
	runtime string

	events        chan *containerd.Event
	eventsContext context.Context
	eventsCancel  func()
}

func (r *Runtime) Create(ctx context.Context, id string, opts containerd.CreateOpts) (containerd.Container, error) {
	path, err := r.newBundle(id, opts.Spec)
	if err != nil {
		return nil, err
	}
	s, err := newShim(path)
	if err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	if err := r.handleEvents(s); err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	sopts := &shim.CreateRequest{
		ID:       id,
		Bundle:   path,
		Runtime:  r.runtime,
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
	}
	for _, m := range opts.Rootfs {
		sopts.Rootfs = append(sopts.Rootfs, &mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	if _, err := s.Create(ctx, sopts); err != nil {
		os.RemoveAll(path)
		return nil, err
	}
	return &Container{
		id:   id,
		shim: s,
	}, nil
}

func (r *Runtime) Delete(ctx context.Context, c containerd.Container) (uint32, error) {
	lc, ok := c.(*Container)
	if !ok {
		return 0, fmt.Errorf("container cannot be cast as *linux.Container")
	}
	rsp, err := lc.shim.Delete(ctx, &shim.DeleteRequest{})
	if err != nil {
		return 0, err
	}
	lc.shim.Exit(ctx, &shim.ExitRequest{})
	return rsp.ExitStatus, r.deleteBundle(lc.id)
}

func (r *Runtime) Containers() ([]containerd.Container, error) {
	dir, err := ioutil.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	var o []containerd.Container
	for _, fi := range dir {
		if !fi.IsDir() {
			continue
		}
		c, err := r.loadContainer(filepath.Join(r.root, fi.Name()))
		if err != nil {
			return nil, err
		}
		o = append(o, c)
	}
	return o, nil
}

func (r *Runtime) Events(ctx context.Context) <-chan *containerd.Event {
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
			log.G(r.eventsContext).WithError(err).Error("get event from shim")
			return
		}
		var et containerd.EventType
		switch e.Type {
		case container.Event_CREATE:
			et = containerd.CreateEvent
		case container.Event_EXEC_ADDED:
			et = containerd.ExecAddEvent
		case container.Event_EXIT:
			et = containerd.ExitEvent
		case container.Event_OOM:
			et = containerd.OOMEvent
		case container.Event_START:
			et = containerd.StartEvent
		}
		r.events <- &containerd.Event{
			Timestamp:  time.Now(),
			Runtime:    runtimeName,
			Type:       et,
			Pid:        e.Pid,
			ID:         e.ID,
			ExitStatus: e.ExitStatus,
		}
	}
}

func (r *Runtime) newBundle(id string, spec []byte) (string, error) {
	path := filepath.Join(r.root, id)
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

func (r *Runtime) deleteBundle(id string) error {
	return os.RemoveAll(filepath.Join(r.root, id))
}

func (r *Runtime) loadContainer(path string) (*Container, error) {
	id := filepath.Base(path)
	s, err := loadShim(path)
	if err != nil {
		return nil, err
	}
	return &Container{
		id:   id,
		shim: s,
	}, nil
}

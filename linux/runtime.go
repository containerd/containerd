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

	"golang.org/x/net/context"
)

const (
	runtimeName    = "linux"
	configFilename = "config.json"
)

func init() {
	containerd.RegisterRuntime(runtimeName, New)
}

func New(root string) (containerd.Runtime, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	c, cancel := context.WithCancel(context.Background())
	return &Runtime{
		root:          root,
		events:        make(chan *containerd.Event, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
	}, nil
}

type Runtime struct {
	root string

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
		Runtime:  "runc",
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

func (r *Runtime) Delete(ctx context.Context, c containerd.Container) error {
	lc, ok := c.(*Container)
	if !ok {
		return fmt.Errorf("container cannot be cast as *linux.Container")
	}
	if _, err := lc.shim.Delete(ctx, &shim.DeleteRequest{}); err != nil {
		return err
	}
	lc.shim.Exit(ctx, &shim.ExitRequest{})
	return r.deleteBundle(lc.id)
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
		c, err := r.loadContainer(fi.Name())
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

// +build linux

package linux

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"

	"github.com/boltdb/bolt"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/events"
	client "github.com/containerd/containerd/linux/shim"
	shim "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	runc "github.com/containerd/go-runc"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"

	"golang.org/x/sys/unix"
)

var (
	ErrTaskNotExists     = errors.New("task does not exist")
	ErrTaskAlreadyExists = errors.New("task already exists")
	pluginID             = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, "linux")
	empty                = &google_protobuf.Empty{}
)

const (
	configFilename = "config.json"
	defaultRuntime = "runc"
	defaultShim    = "containerd-shim"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.RuntimePlugin,
		ID:   "linux",
		Init: New,
		Requires: []plugin.PluginType{
			plugin.TaskMonitorPlugin,
			plugin.MetadataPlugin,
		},
		Config: &Config{
			Shim:    defaultShim,
			Runtime: defaultRuntime,
		},
	})
}

var _ = (runtime.Runtime)(&Runtime{})

type Config struct {
	// Shim is a path or name of binary implementing the Shim GRPC API
	Shim string `toml:"shim,omitempty"`
	// Runtime is a path or name of an OCI runtime used by the shim
	Runtime string `toml:"runtime,omitempty"`
	// NoShim calls runc directly from within the pkg
	NoShim bool `toml:"no_shim,omitempty"`
}

func New(ic *plugin.InitContext) (interface{}, error) {
	if err := os.MkdirAll(ic.Root, 0700); err != nil {
		return nil, err
	}
	monitor, err := ic.Get(plugin.TaskMonitorPlugin)
	if err != nil {
		return nil, err
	}
	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	cfg := ic.Config.(*Config)
	c, cancel := context.WithCancel(ic.Context)
	r := &Runtime{
		root:          ic.Root,
		remote:        !cfg.NoShim,
		shim:          cfg.Shim,
		runtime:       cfg.Runtime,
		events:        make(chan *eventsapi.RuntimeEvent, 2048),
		eventsContext: c,
		eventsCancel:  cancel,
		monitor:       monitor.(runtime.TaskMonitor),
		tasks:         newTaskList(),
		emitter:       events.GetPoster(ic.Context),
		db:            m.(*bolt.DB),
	}
	// set the events output for a monitor if it generates events
	r.monitor.Events(r.events)
	tasks, err := r.restoreTasks(ic.Context)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if err := r.tasks.addWithNamespace(t.namespace, t); err != nil {
			return nil, err
		}
		if err := r.handleEvents(ic.Context, t.shim); err != nil {
			return nil, err
		}
	}
	return r, nil
}

type Runtime struct {
	root    string
	shim    string
	runtime string
	remote  bool

	events        chan *eventsapi.RuntimeEvent
	eventsContext context.Context
	eventsCancel  func()
	monitor       runtime.TaskMonitor
	tasks         *taskList
	emitter       events.Poster
	db            *bolt.DB
}

func (r *Runtime) ID() string {
	return pluginID
}

func (r *Runtime) Create(ctx context.Context, id string, opts runtime.CreateOpts) (_ runtime.Task, err error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	bundle, err := newBundle(filepath.Join(r.root, namespace), namespace, id, opts.Spec)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			bundle.Delete()
		}
	}()
	s, err := bundle.NewShim(ctx, r.shim, r.remote)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if kerr := s.KillShim(ctx); kerr != nil {
				log.G(ctx).WithError(err).Error("failed to kill shim")
			}
		}
	}()
	if err = r.handleEvents(ctx, s); err != nil {
		return nil, err
	}
	sopts := &shim.CreateTaskRequest{
		ID:         id,
		Bundle:     bundle.path,
		Runtime:    r.runtime,
		Stdin:      opts.IO.Stdin,
		Stdout:     opts.IO.Stdout,
		Stderr:     opts.IO.Stderr,
		Terminal:   opts.IO.Terminal,
		Checkpoint: opts.Checkpoint,
		Options:    opts.Options,
	}
	for _, m := range opts.Rootfs {
		sopts.Rootfs = append(sopts.Rootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	if _, err = s.Create(ctx, sopts); err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))
	}
	t := newTask(id, namespace, opts.Spec, s)
	if err := r.tasks.add(ctx, t); err != nil {
		return nil, err
	}
	// after the task is created, add it to the monitor
	if err = r.monitor.Monitor(t); err != nil {
		return nil, err
	}

	var runtimeMounts []*eventsapi.RuntimeMount
	for _, m := range opts.Rootfs {
		runtimeMounts = append(runtimeMounts, &eventsapi.RuntimeMount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	if err := r.emit(ctx, "/runtime/create", &eventsapi.RuntimeCreate{
		ID:     id,
		Bundle: bundle.path,
		RootFS: runtimeMounts,
		IO: &eventsapi.RuntimeIO{
			Stdin:    opts.IO.Stdin,
			Stdout:   opts.IO.Stdout,
			Stderr:   opts.IO.Stderr,
			Terminal: opts.IO.Terminal,
		},
		Checkpoint: opts.Checkpoint,
	}); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *Runtime) Delete(ctx context.Context, c runtime.Task) (*runtime.Exit, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	lc, ok := c.(*Task)
	if !ok {
		return nil, fmt.Errorf("task cannot be cast as *linux.Task")
	}
	if err := r.monitor.Stop(lc); err != nil {
		return nil, err
	}
	rsp, err := lc.shim.Delete(ctx, empty)
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))
	}
	if err := lc.shim.KillShim(ctx); err != nil {
		log.G(ctx).WithError(err).Error("failed to kill shim")
	}
	r.tasks.delete(ctx, lc)

	var (
		bundle = loadBundle(filepath.Join(r.root, namespace, lc.containerID), namespace)
		i      = c.Info()
	)
	if err := r.emit(ctx, "/runtime/delete", &eventsapi.RuntimeDelete{
		ID:         i.ID,
		Runtime:    i.Runtime,
		ExitStatus: rsp.ExitStatus,
		ExitedAt:   rsp.ExitedAt,
	}); err != nil {
		return nil, err
	}
	return &runtime.Exit{
		Status:    rsp.ExitStatus,
		Timestamp: rsp.ExitedAt,
		Pid:       rsp.Pid,
	}, bundle.Delete()
}

func (r *Runtime) Tasks(ctx context.Context) ([]runtime.Task, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	var o []runtime.Task
	tasks, ok := r.tasks.tasks[namespace]
	if !ok {
		return o, nil
	}
	for _, t := range tasks {
		o = append(o, t)
	}
	return o, nil
}

func (r *Runtime) restoreTasks(ctx context.Context) ([]*Task, error) {
	dir, err := ioutil.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	var o []*Task
	for _, namespace := range dir {
		if !namespace.IsDir() {
			continue
		}
		name := namespace.Name()
		log.G(ctx).WithField("namespace", name).Debug("loading tasks in namespace")
		tasks, err := r.loadTasks(ctx, name)
		if err != nil {
			return nil, err
		}
		o = append(o, tasks...)
	}
	return o, nil
}

func (r *Runtime) Get(ctx context.Context, id string) (runtime.Task, error) {
	return r.tasks.get(ctx, id)
}

func (r *Runtime) loadTasks(ctx context.Context, ns string) ([]*Task, error) {
	dir, err := ioutil.ReadDir(filepath.Join(r.root, ns))
	if err != nil {
		return nil, err
	}
	var o []*Task
	for _, path := range dir {
		if !path.IsDir() {
			continue
		}
		id := path.Name()
		bundle := loadBundle(filepath.Join(r.root, ns, id), ns)

		s, err := bundle.Connect(ctx, r.remote)
		if err != nil {
			log.G(ctx).WithError(err).Error("connecting to shim")
			if err := r.terminate(ctx, bundle, ns, id); err != nil {
				log.G(ctx).WithError(err).WithField("bundle", bundle.path).Error("failed to terminate task, leaving bundle for debugging")
				continue
			}
			if err := bundle.Delete(); err != nil {
				log.G(ctx).WithError(err).Error("delete bundle")
			}
			continue
		}
		spec, err := bundle.Spec()
		if err != nil {
			log.G(ctx).WithError(err).Error("load task spec")
		}
		o = append(o, &Task{
			containerID: id,
			shim:        s,
			spec:        spec,
			namespace:   ns,
		})
	}
	return o, nil
}

func (r *Runtime) handleEvents(ctx context.Context, s *client.Client) error {
	events, err := s.Stream(r.eventsContext, &shim.StreamEventsRequest{})
	if err != nil {
		return err
	}
	go r.forward(ctx, events)
	return nil
}

// forward forwards events from a shim to the events service and monitors
func (r *Runtime) forward(ctx context.Context, events shim.Shim_StreamClient) {
	for {
		e, err := events.Recv()
		if err != nil {
			if !strings.HasSuffix(err.Error(), "transport is closing") {
				log.G(r.eventsContext).WithError(err).Error("get event from shim")
			}
			return
		}
		r.events <- e
		if err := r.emit(ctx, "/runtime/"+getTopic(e), e); err != nil {
			return
		}
	}
}

func getTopic(e *eventsapi.RuntimeEvent) string {
	switch e.Type {
	case eventsapi.RuntimeEvent_CREATE:
		return "task-create"
	case eventsapi.RuntimeEvent_START:
		return "task-start"
	case eventsapi.RuntimeEvent_EXEC_ADDED:
		return "task-execadded"
	case eventsapi.RuntimeEvent_OOM:
		return "task-oom"
	case eventsapi.RuntimeEvent_EXIT:
		return "task-exit"
	}
	return ""
}

func (r *Runtime) terminate(ctx context.Context, bundle *bundle, ns, id string) error {
	ctx = namespaces.WithNamespace(ctx, ns)
	rt, err := r.getRuntime(ctx, ns, id)
	if err != nil {
		return err
	}
	if err := rt.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	}); err != nil {
		log.G(ctx).WithError(err).Warnf("delete runtime state %s", id)
	}
	if err := unix.Unmount(filepath.Join(bundle.path, "rootfs"), 0); err != nil {
		log.G(ctx).WithError(err).Warnf("unmount task rootfs %s", id)
	}
	return nil
}

func (r *Runtime) getRuntime(ctx context.Context, ns, id string) (*runc.Runc, error) {
	var c containers.Container
	if err := r.db.View(func(tx *bolt.Tx) error {
		store := metadata.NewContainerStore(tx)
		var err error
		c, err = store.Get(ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	return &runc.Runc{
		// TODO: until we have a way to store/retrieve the original command
		// we can only rely on runc from the default $PATH
		Command:      runc.DefaultCommand,
		LogFormat:    runc.JSON,
		PdeathSignal: unix.SIGKILL,
		Root:         filepath.Join(client.RuncRoot, ns),
	}, nil
}

func (r *Runtime) emit(ctx context.Context, topic string, evt interface{}) error {
	emitterCtx := events.WithTopic(ctx, topic)
	if err := r.emitter.Post(emitterCtx, evt); err != nil {
		return err
	}

	return nil
}

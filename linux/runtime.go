// +build linux

package linux

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/boltdb/bolt"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/identifiers"
	client "github.com/containerd/containerd/linux/shim"
	shim "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/sys"
	runc "github.com/containerd/go-runc"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"golang.org/x/sys/unix"
)

var (
	pluginID = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, "linux")
	empty    = &google_protobuf.Empty{}
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
	// Debug enable debug on the shim
	ShimDebug bool `toml:"shim_debug,omitempty"`
}

func New(ic *plugin.InitContext) (interface{}, error) {
	if err := os.MkdirAll(ic.Root, 0711); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ic.State, 0711); err != nil {
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
	r := &Runtime{
		root:      ic.Root,
		state:     ic.State,
		remote:    !cfg.NoShim,
		shim:      cfg.Shim,
		shimDebug: cfg.ShimDebug,
		runtime:   cfg.Runtime,
		monitor:   monitor.(runtime.TaskMonitor),
		tasks:     runtime.NewTaskList(),
		db:        m.(*bolt.DB),
		address:   ic.Address,
		events:    ic.Events,
	}
	tasks, err := r.restoreTasks(ic.Context)
	if err != nil {
		return nil, err
	}
	// TODO: need to add the tasks to the monitor
	for _, t := range tasks {
		if err := r.tasks.AddWithNamespace(t.namespace, t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

type Runtime struct {
	root      string
	state     string
	shim      string
	shimDebug bool
	runtime   string
	remote    bool
	address   string

	monitor runtime.TaskMonitor
	tasks   *runtime.TaskList
	db      *bolt.DB
	events  *events.Exchange
}

func (r *Runtime) ID() string {
	return pluginID
}

func (r *Runtime) Create(ctx context.Context, id string, opts runtime.CreateOpts) (_ runtime.Task, err error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	if err := identifiers.Validate(id); err != nil {
		return nil, errors.Wrapf(err, "invalid task id")
	}

	bundle, err := newBundle(
		namespace, id,
		filepath.Join(r.state, namespace),
		filepath.Join(r.root, namespace),
		opts.Spec.Value, r.events)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			bundle.Delete()
		}
	}()
	s, err := bundle.NewShim(ctx, r.shim, r.address, r.remote, r.shimDebug, opts, func() {
		t, err := r.tasks.Get(ctx, id)
		if err != nil {
			// Task was never started or was already sucessfully deleted
			return
		}
		lc := t.(*Task)

		// Stop the monitor
		if err := r.monitor.Stop(lc); err != nil {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":        id,
				"namespace": namespace,
			}).Warn("failed to stop monitor")
		}

		log.G(ctx).WithFields(logrus.Fields{
			"id":        id,
			"namespace": namespace,
		}).Warn("cleaning up after killed shim")
		err = r.cleanupAfterDeadShim(context.Background(), bundle, namespace, id, lc.pid, true)
		if err == nil {
			r.tasks.Delete(ctx, lc)
		} else {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":        id,
				"namespace": namespace,
			}).Warn("failed to clen up after killed shim")
		}
	})
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
	cr, err := s.Create(ctx, sopts)
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	t := newTask(id, namespace, int(cr.Pid), s)
	if err := r.tasks.Add(ctx, t); err != nil {
		return nil, err
	}
	// after the task is created, add it to the monitor
	if err = r.monitor.Monitor(t); err != nil {
		r.tasks.Delete(ctx, t)
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
		return nil, errdefs.FromGRPC(err)
	}
	r.tasks.Delete(ctx, lc)
	if err := lc.shim.KillShim(ctx); err != nil {
		log.G(ctx).WithError(err).Error("failed to kill shim")
	}

	bundle := loadBundle(
		filepath.Join(r.state, namespace, lc.id),
		filepath.Join(r.root, namespace, lc.id),
		namespace,
		lc.id,
		r.events,
	)
	if err := bundle.Delete(); err != nil {
		log.G(ctx).WithError(err).Error("failed to delete bundle")
	}
	return &runtime.Exit{
		Status:    rsp.ExitStatus,
		Timestamp: rsp.ExitedAt,
		Pid:       rsp.Pid,
	}, nil
}

func (r *Runtime) Tasks(ctx context.Context) ([]runtime.Task, error) {
	return r.tasks.GetAll(ctx)
}

func (r *Runtime) restoreTasks(ctx context.Context) ([]*Task, error) {
	dir, err := ioutil.ReadDir(r.state)
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
	return r.tasks.Get(ctx, id)
}

func (r *Runtime) loadTasks(ctx context.Context, ns string) ([]*Task, error) {
	dir, err := ioutil.ReadDir(filepath.Join(r.state, ns))
	if err != nil {
		return nil, err
	}
	var o []*Task
	for _, path := range dir {
		if !path.IsDir() {
			continue
		}
		id := path.Name()
		bundle := loadBundle(
			filepath.Join(r.state, ns, id),
			filepath.Join(r.root, ns, id),
			ns, id, r.events)
		s, err := bundle.Connect(ctx, r.remote)
		if err != nil {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":        id,
				"namespace": ns,
			}).Error("connecting to shim")
			pid, _ := runc.ReadPidFile(filepath.Join(bundle.path, client.InitPidFile))
			err := r.cleanupAfterDeadShim(ctx, bundle, ns, id, pid, false)
			if err != nil {
				log.G(ctx).WithError(err).WithField("bundle", bundle.path).
					Error("cleaning up after dead shim")
			}
			continue
		}
		o = append(o, &Task{
			id:        id,
			shim:      s,
			namespace: ns,
		})
	}
	return o, nil
}

func (r *Runtime) cleanupAfterDeadShim(ctx context.Context, bundle *bundle, ns, id string, pid int, reap bool) error {
	ctx = namespaces.WithNamespace(ctx, ns)
	if err := r.terminate(ctx, bundle, ns, id); err != nil {
		return errors.New("failed to terminate task, leaving bundle for debugging")
	}

	if reap {
		// if sub-reaper is set, reap our new child
		if v, err := sys.GetSubreaper(); err == nil && v == 1 {
			reaper.Default.Register(pid, &reaper.Cmd{ExitCh: make(chan struct{})})
			reaper.Default.WaitPid(pid)
			reaper.Default.Delete(pid)
		}
	}

	// Notify Client
	exitedAt := time.Now().UTC()
	r.events.Publish(ctx, runtime.TaskExitEventTopic, &eventsapi.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(unix.SIGKILL),
		ExitedAt:    exitedAt,
	})

	if err := bundle.Delete(); err != nil {
		log.G(ctx).WithError(err).Error("delete bundle")
	}

	r.events.Publish(ctx, runtime.TaskDeleteEventTopic, &eventsapi.TaskDelete{
		ContainerID: id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(unix.SIGKILL),
		ExitedAt:    exitedAt,
	})

	return nil
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
		log.G(ctx).WithError(err).WithFields(logrus.Fields{
			"path": bundle.path,
			"id":   id,
		}).Warnf("unmount task rootfs")
	}
	return nil
}

func (r *Runtime) getRuntime(ctx context.Context, ns, id string) (*runc.Runc, error) {
	if err := r.db.View(func(tx *bolt.Tx) error {
		store := metadata.NewContainerStore(tx)
		var err error
		_, err = store.Get(ctx, id)
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

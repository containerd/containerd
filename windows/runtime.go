// +build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/boltdb/bolt"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	containerdtypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
	"github.com/containerd/containerd/windows/hcsshimopts"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const (
	runtimeName              = "windows"
	hcsshimOwner             = "containerd"
	defaultTerminateDuration = 5 * time.Minute
)

var (
	pluginID = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, runtimeName)
)

var _ = (runtime.Runtime)(&windowsRuntime{})

func init() {
	plugin.Register(&plugin.Registration{
		ID:   runtimeName,
		Type: plugin.RuntimePlugin,
		Init: New,
		Requires: []plugin.PluginType{
			plugin.MetadataPlugin,
		},
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	if err := os.MkdirAll(ic.Root, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", ic.Root)
	}

	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}

	r := &windowsRuntime{
		root:    ic.Root,
		pidPool: newPidPool(),

		events:  make(chan interface{}, 4096),
		emitter: ic.Emitter,
		// TODO(mlaventure): windows needs a stat monitor
		monitor: nil,
		tasks:   runtime.NewTaskList(),
		db:      m.(*bolt.DB),
	}

	// Load our existing containers and kill/delete them. We don't support
	// reattaching to them
	r.cleanup(ic.Context)

	return r, nil
}

type windowsRuntime struct {
	sync.Mutex

	root    string
	pidPool *pidPool

	emitter *events.Emitter
	events  chan interface{}

	monitor runtime.TaskMonitor
	tasks   *runtime.TaskList
	db      *bolt.DB
}

func (r *windowsRuntime) ID() string {
	return pluginID
}

func (r *windowsRuntime) Create(ctx context.Context, id string, opts runtime.CreateOpts) (runtime.Task, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	s, err := typeurl.UnmarshalAny(opts.Spec)
	if err != nil {
		return nil, err
	}
	spec := s.(*specs.Spec)

	var createOpts *hcsshimopts.CreateOptions
	if opts.Options != nil {
		o, err := typeurl.UnmarshalAny(opts.Options)
		if err != nil {
			return nil, err
		}
		createOpts = o.(*hcsshimopts.CreateOptions)
	} else {
		createOpts = &hcsshimopts.CreateOptions{}
	}

	if createOpts.TerminateDuration == 0 {
		createOpts.TerminateDuration = defaultTerminateDuration
	}

	t, err := r.newTask(ctx, r.emitter, namespace, id, spec, opts.IO, createOpts)
	if err != nil {
		return nil, err
	}

	r.tasks.Add(ctx, t)

	r.emitter.Post(events.WithTopic(ctx, "/tasks/create"), &eventsapi.TaskCreate{
		ContainerID: id,
		IO: &eventsapi.TaskIO{
			Stdin:    opts.IO.Stdin,
			Stdout:   opts.IO.Stdout,
			Stderr:   opts.IO.Stderr,
			Terminal: opts.IO.Terminal,
		},
		Pid:    t.pid,
		Rootfs: t.rootfs,
		// TODO: what should be in Bundle for windows?
	})

	return t, nil
}

func (r *windowsRuntime) Get(ctx context.Context, id string) (runtime.Task, error) {
	return r.tasks.Get(ctx, id)
}

func (r *windowsRuntime) Tasks(ctx context.Context) ([]runtime.Task, error) {
	return r.tasks.GetAll(ctx)
}

func (r *windowsRuntime) Delete(ctx context.Context, t runtime.Task) (*runtime.Exit, error) {
	wt, ok := t.(*task)
	if !ok {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument, "no a windows task")
	}

	// TODO(mlaventure): stop monitor on this task

	state, _ := wt.State(ctx)
	switch state.Status {
	case runtime.StoppedStatus, runtime.CreatedStatus:
		// if it's stopped or in created state, we need to shutdown the
		// container before removing it
		if err := wt.stop(ctx); err != nil {
			return nil, err
		}
	default:
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition,
			"cannot delete a non-stopped task")
	}

	var rtExit *runtime.Exit
	if p := wt.getProcess(t.ID()); p != nil {
		ec, ea, err := p.ExitCode()
		if err != nil {
			return nil, err
		}
		rtExit = &runtime.Exit{
			Pid:       wt.pid,
			Status:    ec,
			Timestamp: ea,
		}
	} else {
		rtExit = &runtime.Exit{
			Pid:       wt.pid,
			Status:    255,
			Timestamp: time.Now(),
		}
	}

	wt.cleanup()
	r.tasks.Delete(ctx, t)

	r.emitter.Post(events.WithTopic(ctx, "/tasks/delete"), &eventsapi.TaskDelete{
		ContainerID: wt.id,
		Pid:         wt.pid,
		ExitStatus:  rtExit.Status,
		ExitedAt:    rtExit.Timestamp,
	})

	// We were never started, return failure
	return rtExit, nil
}

func (r *windowsRuntime) newTask(ctx context.Context, emitter *events.Emitter, namespace, id string, spec *specs.Spec, io runtime.IO, createOpts *hcsshimopts.CreateOptions) (*task, error) {
	var (
		err  error
		pset *pipeSet
	)

	if pset, err = newPipeSet(ctx, io); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			pset.Close()
		}
	}()

	var pid uint32
	if pid, err = r.pidPool.Get(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			r.pidPool.Put(pid)
		}
	}()

	var (
		conf *hcsshim.ContainerConfig
		nsid = namespace + "-" + id
	)
	if conf, err = newContainerConfig(ctx, hcsshimOwner, nsid, spec); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			removeLayer(ctx, conf.LayerFolderPath)
		}
	}()

	// TODO: remove this once we have a windows snapshotter
	// Store the LayerFolder in the db so we can clean it if we die
	if err = r.db.Update(func(tx *bolt.Tx) error {
		s := newLayerFolderStore(tx)
		return s.Create(nsid, conf.LayerFolderPath)
	}); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if dbErr := r.db.Update(func(tx *bolt.Tx) error {
				s := newLayerFolderStore(tx)
				return s.Delete(nsid)
			}); dbErr != nil {
				log.G(ctx).WithField("id", id).
					Error("failed to remove key from metadata")
			}
		}
	}()

	ctr, err := hcsshim.CreateContainer(nsid, conf)
	if err != nil {
		return nil, errors.Wrapf(err, "hcsshim failed to create task")
	}
	defer func() {
		if err != nil {
			ctr.Terminate()
			ctr.Wait()
			ctr.Close()
		}
	}()

	if err = ctr.Start(); err != nil {
		return nil, errors.Wrap(err, "hcsshim failed to spawn task")
	}

	var rootfs []*containerdtypes.Mount
	for _, l := range append([]string{conf.LayerFolderPath}, spec.Windows.LayerFolders...) {
		rootfs = append(rootfs, &containerdtypes.Mount{
			Type:   "windows-layer",
			Source: l,
		})
	}

	return &task{
		id:                id,
		namespace:         namespace,
		pid:               pid,
		io:                pset,
		status:            runtime.CreatedStatus,
		initSpec:          spec.Process,
		processes:         make(map[string]*process),
		hyperV:            spec.Windows.HyperV != nil,
		rootfs:            rootfs,
		emitter:           emitter,
		pidPool:           r.pidPool,
		hcsContainer:      ctr,
		terminateDuration: createOpts.TerminateDuration,
	}, nil
}

func (r *windowsRuntime) cleanup(ctx context.Context) {
	cp, err := hcsshim.GetContainers(hcsshim.ComputeSystemQuery{
		Types:  []string{"Container"},
		Owners: []string{hcsshimOwner},
	})
	if err != nil {
		log.G(ctx).Warn("failed to retrieve running containers")
		return
	}

	for _, p := range cp {
		container, err := hcsshim.OpenContainer(p.ID)
		if err != nil {
			log.G(ctx).Warnf("failed open container %s", p.ID)
			continue
		}

		err = container.Terminate()
		if err == nil || hcsshim.IsPending(err) || hcsshim.IsAlreadyStopped(err) {
			container.Wait()
		}
		container.Close()

		// TODO: remove this once we have a windows snapshotter
		var layerFolderPath string
		if err := r.db.View(func(tx *bolt.Tx) error {
			s := newLayerFolderStore(tx)
			l, e := s.Get(p.ID)
			if err == nil {
				layerFolderPath = l
			}
			return e
		}); err == nil && layerFolderPath != "" {
			removeLayer(ctx, layerFolderPath)
			if dbErr := r.db.Update(func(tx *bolt.Tx) error {
				s := newLayerFolderStore(tx)
				return s.Delete(p.ID)
			}); dbErr != nil {
				log.G(ctx).WithField("id", p.ID).
					Error("failed to remove key from metadata")
			}
		} else {
			log.G(ctx).WithField("id", p.ID).
				Debug("key not found in metadata, R/W layer may be leaked")
		}

	}
}

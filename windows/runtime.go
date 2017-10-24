// +build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/windows/hcsshimtypes"
	"github.com/containerd/typeurl"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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
		ID:     runtimeName,
		Type:   plugin.RuntimePlugin,
		InitFn: New,
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	if err := os.MkdirAll(ic.Root, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", ic.Root)
	}

	r := &windowsRuntime{
		root:    ic.Root,
		pidPool: newPidPool(),

		events:    make(chan interface{}, 4096),
		publisher: ic.Events,
		// TODO(mlaventure): windows needs a stat monitor
		monitor: nil,
		tasks:   runtime.NewTaskList(),
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

	publisher events.Publisher
	events    chan interface{}

	monitor runtime.TaskMonitor
	tasks   *runtime.TaskList
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

	var createOpts *hcsshimtypes.CreateOptions
	if opts.Options != nil {
		o, err := typeurl.UnmarshalAny(opts.Options)
		if err != nil {
			return nil, err
		}
		createOpts = o.(*hcsshimtypes.CreateOptions)
	} else {
		createOpts = &hcsshimtypes.CreateOptions{}
	}

	if createOpts.TerminateDuration == 0 {
		createOpts.TerminateDuration = defaultTerminateDuration
	}

	for _, m := range opts.Rootfs {
		spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, m.Source)
	}

	return r.newTask(ctx, namespace, id, opts.Rootfs, spec, opts.IO, createOpts)
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

	var (
		err           error
		needServicing bool
		state, _      = wt.State(ctx)
	)
	switch state.Status {
	case runtime.StoppedStatus:
		// Only try to service a container if it was started and it's not a
		// servicing task itself
		if wt.servicing == false {
			needServicing, err = wt.hcsContainer.HasPendingUpdates()
			if err != nil {
				needServicing = false
				log.G(ctx).WithError(err).
					WithFields(logrus.Fields{"id": wt.id, "pid": wt.pid}).
					Error("failed to check if container needs servicing")
			}
		}
		fallthrough
	case runtime.CreatedStatus:
		// if it's stopped or in created state, we need to shutdown the
		// container before removing it
		if err = wt.stop(ctx); err != nil {
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
			Timestamp: time.Now().UTC(),
		}
	}

	wt.cleanup()
	r.tasks.Delete(ctx, t)

	r.publisher.Publish(ctx,
		runtime.TaskDeleteEventTopic,
		&eventsapi.TaskDelete{
			ContainerID: wt.id,
			Pid:         wt.pid,
			ExitStatus:  rtExit.Status,
			ExitedAt:    rtExit.Timestamp,
		})

	if needServicing {
		ns, _ := namespaces.Namespace(ctx)
		serviceCtx := log.WithLogger(context.Background(), log.GetLogger(ctx))
		serviceCtx = namespaces.WithNamespace(serviceCtx, ns)
		r.serviceTask(serviceCtx, ns, wt.id+"_servicing", wt.rootfs, wt.spec)
	}

	if err := mount.UnmountAll(wt.rootfs[0].Source, 0); err != nil {
		log.G(ctx).WithError(err).WithField("path", wt.rootfs[0].Source).
			Warn("failed to unmount rootfs on failure")
	}

	// We were never started, return failure
	return rtExit, nil
}

func (r *windowsRuntime) newTask(ctx context.Context, namespace, id string, rootfs []mount.Mount, spec *specs.Spec, io runtime.IO, createOpts *hcsshimtypes.CreateOptions) (*task, error) {
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

	if err := mount.All(rootfs, ""); err != nil {
		return nil, errors.Wrap(err, "failed to mount rootfs")
	}
	defer func() {
		if err != nil {
			if err := mount.UnmountAll(rootfs[0].Source, 0); err != nil {
				log.G(ctx).WithError(err).WithField("path", rootfs[0].Source).
					Warn("failed to unmount rootfs on failure")
			}
		}
	}()

	var (
		conf *hcsshim.ContainerConfig
		nsid = namespace + "-" + id
	)
	if conf, err = newWindowsContainerConfig(ctx, hcsshimOwner, nsid, spec); err != nil {
		return nil, err
	}

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

	t := &task{
		id:                id,
		namespace:         namespace,
		pid:               pid,
		io:                pset,
		status:            runtime.CreatedStatus,
		spec:              spec,
		processes:         make(map[string]*process),
		hyperV:            spec.Windows.HyperV != nil,
		publisher:         r.publisher,
		rwLayer:           conf.LayerFolderPath,
		rootfs:            rootfs,
		pidPool:           r.pidPool,
		hcsContainer:      ctr,
		terminateDuration: createOpts.TerminateDuration,
	}
	// Create the new process but don't start it
	pconf := newWindowsProcessConfig(t.spec.Process, t.io)
	if _, err = t.newProcess(ctx, t.id, pconf, t.io); err != nil {
		return nil, err
	}
	r.tasks.Add(ctx, t)

	r.publisher.Publish(ctx,
		runtime.TaskCreateEventTopic,
		&eventsapi.TaskCreate{
			ContainerID: id,
			IO: &eventsapi.TaskIO{
				Stdin:    io.Stdin,
				Stdout:   io.Stdout,
				Stderr:   io.Stderr,
				Terminal: io.Terminal,
			},
			Pid: t.pid,
			//Rootfs: rootfs,
			// TODO: what should be in Bundle for windows?
		})

	return t, nil
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
	}
}

func (r *windowsRuntime) serviceTask(ctx context.Context, namespace, id string, rootfs []mount.Mount, spec *specs.Spec) {
	var (
		err        error
		t          *task
		io         runtime.IO
		createOpts = &hcsshimtypes.CreateOptions{
			TerminateDuration: defaultTerminateDuration,
		}
	)

	t, err = r.newTask(ctx, namespace, id, rootfs, spec, io, createOpts)
	if err != nil {
		log.G(ctx).WithError(err).WithField("id", id).
			Warn("failed to created servicing task")
		return
	}
	t.servicing = true

	err = t.Start(ctx)
	switch err {
	case nil:
		<-t.getProcess(id).exitCh
	default:
		log.G(ctx).WithError(err).WithField("id", id).
			Warn("failed to start servicing task")
	}

	if _, err = r.Delete(ctx, t); err != nil {
		log.G(ctx).WithError(err).WithField("id", id).
			Warn("failed to stop servicing task")
	}
}

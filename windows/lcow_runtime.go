// +build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	lcowRuntimeName = "lcow"
)

var (
	lcowPluginID = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, lcowRuntimeName)
)

var _ = (runtime.Runtime)(&LCOWRuntime{})

func init() {
	plugin.Register(&plugin.Registration{
		ID:   lcowRuntimeName,
		Type: plugin.RuntimePlugin,
		Init: NewLCOWPlugin,
		Requires: []plugin.PluginType{
			plugin.MetadataPlugin,
		},
	})
}

func NewLCOWPlugin(ic *plugin.InitContext) (interface{}, error) {
	if err := os.MkdirAll(ic.Root, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create root directory at %s", ic.Root)
	}

	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}

	cd := filepath.Join(ic.Root, cacheDirectory)
	sd := filepath.Join(ic.Root, scratchDirectory)

	// Make sure the scratch directory is created under dataRoot
	if err := os.MkdirAll(sd, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create scratch directory at %s", ic.Root)
	}

	// Make sure the cache directory is created under dataRoot
	if err := os.MkdirAll(cd, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create cache directory at %s", ic.Root)
	}

	r := &LCOWRuntime{
		root:    ic.Root,
		pidPool: newPidPool(),

		events:    make(chan interface{}, 4096),
		publisher: ic.Events,
		// TODO(mlaventure): lcow needs a stat monitor
		monitor: nil,
		tasks:   runtime.NewTaskList(),
		db:      m.(*bolt.DB),

		cachedSandboxFile: filepath.Join(cd, sandboxFilename),
		cachedScratchFile: filepath.Join(cd, scratchFilename),
		cache:             make(map[string]*cacheItem),
		serviceVms:        make(map[string]*serviceVMItem),
		globalMode:        false,
	}

	// Load our existing containers and kill/delete them. We don't support
	// reattaching to them
	r.cleanup(ic.Context)

	return r, nil
}

type LCOWRuntime struct {
	sync.Mutex

	root    string
	pidPool *pidPool

	publisher events.Publisher
	events    chan interface{}

	monitor runtime.TaskMonitor
	tasks   *runtime.TaskList
	db      *bolt.DB

	cachedSandboxFile  string                    // Location of the local default-sized cached sandbox.
	cachedSandboxMutex sync.Mutex                // Protects race conditions from multiple threads creating the cached sandbox.
	cachedScratchFile  string                    // Location of the local cached empty scratch space. This is used as temporary diesk space by the utility VM.
	cachedScratchMutex sync.Mutex                // Protects race conditions from multiple threads creating the cached scratch.
	serviceVmsMutex    sync.Mutex                // Protects add/updates/delete to the serviceVMs map.
	serviceVms         map[string]*serviceVMItem // Map of the configs representing the service VM(s) we are running.
	globalMode         bool                      // Indicates if running in an unsafe/global service VM mode.

	// NOTE: It is OK to use a cache here because Windows does not support
	// restoring containers when the daemon dies.

	cacheMutex sync.Mutex            // Protects add/update/deletes to cache.
	cache      map[string]*cacheItem // Map holding a cache of all the IDs we've mounted/unmounted.
}

func (r *LCOWRuntime) ID() string {
	return lcowPluginID
}

func (r *LCOWRuntime) Create(ctx context.Context, id string, opts runtime.CreateOpts) (runtime.Task, error) {
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

	return r.newTask(ctx, namespace, id, spec, opts.IO, createOpts)
}

func (r *LCOWRuntime) Get(ctx context.Context, id string) (runtime.Task, error) {
	return r.tasks.Get(ctx, id)
}

func (r *LCOWRuntime) Tasks(ctx context.Context) ([]runtime.Task, error) {
	return r.tasks.GetAll(ctx)
}

func (r *LCOWRuntime) Delete(ctx context.Context, t runtime.Task) (*runtime.Exit, error) {
	wt, ok := t.(*task)
	if !ok {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument, "not an LCOW task")
	}

	// TODO(mlaventure): stop monitor on this task

	var (
		err      error
		state, _ = wt.State(ctx)
	)
	switch state.Status {
	case runtime.StoppedStatus:
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
			Timestamp: time.Now(),
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

	// We were never started, return failure
	return rtExit, nil
}

func (r *LCOWRuntime) newTask(ctx context.Context, namespace, id string, spec *specs.Spec, io runtime.IO, createOpts *hcsshimopts.CreateOptions) (*task, error) {
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
	if conf, err = newLinuxConfig(ctx, lcowPluginID, nsid, spec); err != nil {
		return nil, err
	}

	if err := r.CreateRWLayer(ctx, id, spec.Windows.LayerFolders[0]); err != nil {
		return nil, err
	}
	conf.LayerFolderPath = r.containerLayerDir(id)
	defer func() {
		if err != nil {
			removeLinuxLayer(ctx, conf.LayerFolderPath)
		}
	}()

	// TODO: remove this once we have an LCOW snapshotter
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
		time.Sleep(15 * time.Second)
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
		publisher:         r.publisher,
		rwLayer:           conf.LayerFolderPath,
		pidPool:           r.pidPool,
		isWindows:         false,
		hyperV:            true,
		hcsContainer:      ctr,
		terminateDuration: createOpts.TerminateDuration,
	}
	r.tasks.Add(ctx, t)

	var rootfs []*containerdtypes.Mount
	for _, l := range append([]string{t.rwLayer}, spec.Windows.LayerFolders...) {
		rootfs = append(rootfs, &containerdtypes.Mount{
			Type:   "lcow-layer",
			Source: l,
		})
	}

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
			Pid:    t.pid,
			Rootfs: rootfs,
			// TODO: what should be in Bundle for windows?
		})

	return t, nil
}

func (r *LCOWRuntime) cleanup(ctx context.Context) {
	cleanupRunningContainers(ctx, r.db, lcowPluginID, removeLayer)
}

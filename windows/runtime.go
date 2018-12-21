// +build windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package windows

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/windows/hcsshimtypes"
	"github.com/containerd/typeurl"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
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

var _ = (runtime.PlatformRuntime)(&windowsRuntime{})

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

// New returns a new Windows runtime
func New(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}

	if err := os.MkdirAll(ic.Root, 0700); err != nil {
		return nil, errors.Wrapf(err, "could not create state directory at %s", ic.Root)
	}
	r := &windowsRuntime{
		root:    ic.Root,
		pidPool: newPidPool(),

		events:    make(chan interface{}, 4096),
		publisher: ic.Events,
		tasks:     runtime.NewTaskList(),
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

	tasks *runtime.TaskList
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
	spec := s.(*runtimespec.Spec)

	var createOpts *hcsshimtypes.CreateOptions
	if opts.TaskOptions != nil {
		o, err := typeurl.UnmarshalAny(opts.TaskOptions)
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

	if len(opts.Rootfs) == 0 {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument, "rootfs was not provided to container create")
	}
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, opts.Rootfs[0].Source)
	parentLayerPaths, err := opts.Rootfs[0].GetParentPaths()
	if err != nil {
		return nil, err
	}
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, parentLayerPaths...)

	return r.newTask(ctx, namespace, id, opts.Rootfs, spec, opts.IO, createOpts)
}

func (r *windowsRuntime) Get(ctx context.Context, id string) (runtime.Task, error) {
	return r.tasks.Get(ctx, id)
}

func (r *windowsRuntime) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	return r.tasks.GetAll(ctx, all)
}

func (r *windowsRuntime) Add(ctx context.Context, task runtime.Task) error {
	return r.tasks.Add(ctx, task)
}

func (r *windowsRuntime) Delete(ctx context.Context, id string) {
	r.tasks.Delete(ctx, id)
}

func (r *windowsRuntime) newTask(ctx context.Context, namespace, id string, rootfs []mount.Mount, spec *runtimespec.Spec, io runtime.IO, createOpts *hcsshimtypes.CreateOptions) (*task, error) {
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
		tasks:             r.tasks,
	}
	// Create the new process but don't start it
	pconf := newWindowsProcessConfig(t.spec.Process, t.io)
	if _, err = t.newProcess(ctx, t.id, pconf, t.io); err != nil {
		return nil, err
	}
	r.tasks.Add(ctx, t)

	var eventRootfs []*types.Mount
	for _, m := range rootfs {
		eventRootfs = append(eventRootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}

	r.publisher.Publish(ctx,
		runtime.TaskCreateEventTopic,
		&eventstypes.TaskCreate{
			ContainerID: id,
			IO: &eventstypes.TaskIO{
				Stdin:    io.Stdin,
				Stdout:   io.Stdout,
				Stderr:   io.Stderr,
				Terminal: io.Terminal,
			},
			Pid:    t.pid,
			Rootfs: eventRootfs,
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

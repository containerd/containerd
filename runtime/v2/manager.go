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

package v2

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/timeout"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/task"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Config for the v2 runtime
type Config struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.RuntimePluginV2,
		ID:   "shim",
		Requires: []plugin.Type{
			plugin.EventPlugin,
			plugin.MetadataPlugin,
		},
		Config: &Config{
			Platforms: defaultPlatforms(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			supportedPlatforms, err := parsePlatforms(ic.Config.(*Config).Platforms)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = supportedPlatforms
			if err := os.MkdirAll(ic.Root, 0711); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(ic.State, 0711); err != nil {
				return nil, err
			}
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ep, err := ic.GetByID(plugin.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}
			cs := metadata.NewContainerStore(m.(*metadata.DB))
			events := ep.(*exchange.Exchange)

			return NewShimManager(ic.Context, ic.Root, ic.State, ic.Address, ic.TTRPCAddress, events, cs)
		},
	})

	plugin.Register(&plugin.Registration{
		Type: plugin.RuntimePluginV2,
		ID:   "task",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimInstance, err := ic.GetByID(plugin.RuntimePluginV2, "shim")
			if err != nil {
				return nil, err
			}

			shimManager := shimInstance.(*ShimManager)
			return NewTaskManager(shimManager), nil
		},
	})
}

// NewShimManager creates a manager for v2 shims
func NewShimManager(ctx context.Context, root, state, containerdAddress, containerdTTRPCAddress string, events *exchange.Exchange, cs containers.Store) (*ShimManager, error) {
	for _, d := range []string{root, state} {
		if err := os.MkdirAll(d, 0711); err != nil {
			return nil, err
		}
	}
	m := &ShimManager{
		root:                   root,
		state:                  state,
		containerdAddress:      containerdAddress,
		containerdTTRPCAddress: containerdTTRPCAddress,
		list:                   runtime.NewTaskList(),
		events:                 events,
		containers:             cs,
	}
	if err := m.loadExistingShims(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// ShimManager manages currently running shim processes.
// It is mainly responsible for launching new shims and for proper shutdown and cleanup of existing instances.
// The manager is unaware of the underlying services shim provides and lets higher level services consume them,
// but don't care about lifecycle management.
type ShimManager struct {
	root                   string
	state                  string
	containerdAddress      string
	containerdTTRPCAddress string
	list                   *runtime.TaskList
	events                 *exchange.Exchange
	containers             containers.Store
}

// ID of the shim manager
func (m *ShimManager) ID() string {
	return fmt.Sprintf("%s.%s", plugin.RuntimePluginV2, "shim")
}

// Start launches a new shim process
func (m *ShimManager) Start(ctx context.Context, id string, opts runtime.CreateOpts) (_ *shimTask, retErr error) {
	bundle, err := NewBundle(ctx, m.root, m.state, id, opts.Spec.Value)
	if err != nil {
		return nil, err
	}

	defer func() {
		if retErr != nil {
			bundle.Delete()
		}
	}()

	shim, err := m.startShim(ctx, bundle, id, opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			m.deleteShim(shim)
		}
	}()

	// NOTE: This wrap no longer be required once we move task service to client.
	shimTask := &shimTask{
		shim: shim,
		task: task.NewTaskClient(shim.client),
	}

	if err := m.list.Add(ctx, shimTask); err != nil {
		return nil, errors.Wrap(err, "failed to add task")
	}

	return shimTask, nil
}

func (m *ShimManager) startShim(ctx context.Context, bundle *Bundle, id string, opts runtime.CreateOpts) (*shim, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	topts := opts.TaskOptions
	if topts == nil {
		topts = opts.RuntimeOptions
	}

	b := shimBinary(bundle, opts.Runtime, m.containerdAddress, m.containerdTTRPCAddress)
	shim, err := b.Start(ctx, topts, func() {
		log.G(ctx).WithField("id", id).Info("shim disconnected")

		cleanupAfterDeadShim(context.Background(), id, ns, m.list, m.events, b)
		// Remove self from the runtime task list. Even though the cleanupAfterDeadShim()
		// would publish taskExit event, but the shim.Delete() would always failed with ttrpc
		// disconnect and there is no chance to remove this dead task from runtime task lists.
		// Thus it's better to delete it here.
		m.list.Delete(ctx, id)
	})
	if err != nil {
		return nil, errors.Wrap(err, "start failed")
	}

	return shim, nil
}

// deleteShim attempts to properly delete and cleanup shim after error
func (m *ShimManager) deleteShim(shim *shim) error {
	dctx, cancel := timeout.WithContext(context.Background(), cleanupTimeout)
	defer cancel()

	m.list.Delete(dctx, shim.ID())
	return shim.delete(dctx)
}

// Delete shuts down shim process
func (m *ShimManager) Delete(ctx context.Context, id string) error {
	shim, err := m.Get(ctx, id)
	if err != nil {
		return err
	}

	return shim.delete(ctx)
}

// Get retrieves shim instance by id
func (m *ShimManager) Get(ctx context.Context, id string) (*shim, error) {
	item, err := m.list.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	shimTask := item.(*shimTask)
	return shimTask.shim, nil
}

func (m *ShimManager) loadExistingShims(ctx context.Context) error {
	nsDirs, err := ioutil.ReadDir(m.state)
	if err != nil {
		return err
	}
	for _, nsd := range nsDirs {
		if !nsd.IsDir() {
			continue
		}
		ns := nsd.Name()
		// skip hidden directories
		if len(ns) > 0 && ns[0] == '.' {
			continue
		}
		log.G(ctx).WithField("namespace", ns).Debug("loading list in namespace")
		if err := m.loadShims(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("loading list in namespace")
			continue
		}
		if err := m.cleanupWorkDirs(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("cleanup working directory in namespace")
			continue
		}
	}
	return nil
}

func (m *ShimManager) loadShims(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	shimDirs, err := ioutil.ReadDir(filepath.Join(m.state, ns))
	if err != nil {
		return err
	}
	for _, sd := range shimDirs {
		if !sd.IsDir() {
			continue
		}
		id := sd.Name()
		// skip hidden directories
		if len(id) > 0 && id[0] == '.' {
			continue
		}
		bundle, err := LoadBundle(ctx, m.state, id)
		if err != nil {
			// fine to return error here, it is a programmer error if the context
			// does not have a namespace
			return err
		}
		// fast path
		bf, err := ioutil.ReadDir(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}
		container, err := m.container(ctx, id)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("loading container %s", id)
			if err := mount.UnmountAll(filepath.Join(bundle.Path, "rootfs"), 0); err != nil {
				log.G(ctx).WithError(err).Errorf("forceful unmount of rootfs %s", id)
			}
			bundle.Delete()
			continue
		}
		binaryCall := shimBinary(bundle, container.Runtime.Name, m.containerdAddress, m.containerdTTRPCAddress)
		shim, err := loadShim(ctx, bundle, func() {
			log.G(ctx).WithField("id", id).Info("shim disconnected")

			cleanupAfterDeadShim(context.Background(), id, ns, m.list, m.events, binaryCall)
			// Remove self from the runtime task list.
			m.list.Delete(ctx, id)
		})
		if err != nil {
			cleanupAfterDeadShim(ctx, id, ns, m.list, m.events, binaryCall)
			continue
		}
		m.list.Add(ctx, shim)
	}
	return nil
}

func (m *ShimManager) container(ctx context.Context, id string) (*containers.Container, error) {
	container, err := m.containers.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &container, nil
}

func (m *ShimManager) cleanupWorkDirs(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	dirs, err := ioutil.ReadDir(filepath.Join(m.root, ns))
	if err != nil {
		return err
	}
	for _, d := range dirs {
		// if the task was not loaded, cleanup and empty working directory
		// this can happen on a reboot where /run for the bundle state is cleaned up
		// but that persistent working dir is left
		if _, err := m.list.Get(ctx, d.Name()); err != nil {
			path := filepath.Join(m.root, ns, d.Name())
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
	}
	return nil
}

func parsePlatforms(platformStr []string) ([]ocispec.Platform, error) {
	p := make([]ocispec.Platform, len(platformStr))
	for i, v := range platformStr {
		parsed, err := platforms.Parse(v)
		if err != nil {
			return nil, err
		}
		p[i] = parsed
	}
	return p, nil
}

// TaskManager wraps task service client on top of shim manager.
type TaskManager struct {
	shims *ShimManager
}

// NewTaskManager creates a new task manager instance.
func NewTaskManager(shims *ShimManager) *TaskManager {
	return &TaskManager{
		shims: shims,
	}
}

// ID of the task manager
func (m *TaskManager) ID() string {
	return fmt.Sprintf("%s.%s", plugin.RuntimePluginV2, "task")
}

// Create launches new shim instance and creates new task
func (m *TaskManager) Create(ctx context.Context, taskID string, opts runtime.CreateOpts) (runtime.Task, error) {
	shim, err := m.shims.Start(ctx, taskID, opts)
	if err != nil {
		return nil, err
	}

	t, err := shim.Create(ctx, opts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create shim task")
	}

	return t, nil
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	return m.shims.list.Get(ctx, id)
}

// Tasks lists all list
func (m *TaskManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	return m.shims.list.GetAll(ctx, all)
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	item, err := m.shims.list.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	dctx, cancel := timeout.WithContext(context.Background(), cleanupTimeout)
	defer cancel()

	// NOTE: we don't call ShimManager.Delete here in order to preserse existing clean up logic.
	// shimTask.delete will call shim.delete internally.
	shim := item.(*shimTask)

	exit, err := shim.delete(dctx, func(ctx context.Context, id string) {
		m.shims.list.Delete(ctx, id)
	})

	if err != nil {
		if errdefs.IsDeadlineExceeded(err) {
			dctx, cancel = timeout.WithContext(context.Background(), cleanupTimeout)
			defer cancel()
		}

		shim.Shutdown(dctx)
		shim.Close()

		return nil, err
	}

	return exit, nil
}

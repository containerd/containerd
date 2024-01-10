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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/metadata"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/runtime"
)

// Config for the v2 runtime
type Config struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
	// SchedCore enabled linux core scheduling
	SchedCore bool `toml:"sched_core"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.RuntimePluginV2,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
			plugins.ShimPlugin,
			plugins.SandboxControllerPlugin,
		},
		RequiresExcludes: []string{
			plugins.SandboxControllerPlugin.String() + "." + "podsandbox",
		},
		Config: &Config{
			Platforms: defaultPlatforms(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*Config)
			supportedPlatforms, err := platforms.ParseAll(config.Platforms)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = supportedPlatforms

			m, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ss := metadata.NewSandboxStore(m.(*metadata.DB))

			client, err := containerd.New(
				"",
				containerd.WithServices(containerd.WithSandboxStore(ss)),
				containerd.WithInMemorySandboxControllers(ic),
			)
			if err != nil {
				return nil, err
			}
			sandboxedTaskManager := NewSandboxedTaskManager(client)

			shimManagerI, err := ic.GetByID(plugins.ShimPlugin, "shim")
			if err != nil {
				return nil, err
			}
			shimManager := shimManagerI.(*ShimManager)
			if err != nil {
				return nil, err
			}

			root, state := ic.Properties[plugins.PropertyRootDir], ic.Properties[plugins.PropertyStateDir]
			for _, d := range []string{root, state} {
				if err := os.MkdirAll(d, 0711); err != nil {
					return nil, err
				}
			}
			return NewTaskManager(ic.Context, root, state, shimManager, sandboxedTaskManager)
		},
	})
}

// TaskManager wraps task service client on top of shim manager.
type TaskManager struct {
	root                 string
	state                string
	shimManager          *ShimManager
	sandboxedTaskManager *SandboxedTaskManager
}

// NewTaskManager creates a new task manager instance.
func NewTaskManager(ctx context.Context, root, state string, shims *ShimManager, sandboxes *SandboxedTaskManager) (*TaskManager, error) {
	m := &TaskManager{
		root:                 root,
		state:                state,
		shimManager:          shims,
		sandboxedTaskManager: sandboxes,
	}

	if err := m.loadExistingTasks(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// ID of the task manager
func (m *TaskManager) ID() string {
	return plugins.RuntimePluginV2.String() + ".task"
}

// Create launches new shim instance and creates new task
func (m *TaskManager) Create(ctx context.Context, taskID string, opts runtime.CreateOpts) (t runtime.Task, retErr error) {
	bundle, err := NewBundle(ctx, m.root, m.state, taskID, opts.Spec)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			bundle.Delete()
		}
	}()

	if len(opts.SandboxID) > 0 {
		// if err==ErrCanNotHandle, we should fallback to the shim
		if t, err := m.sandboxedTaskManager.Create(ctx, taskID, bundle, opts); err != ErrCanNotHandle {
			return t, err
		}
	}
	return m.shimManager.Create(ctx, taskID, bundle, opts)
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	t, err := m.sandboxedTaskManager.Get(ctx, id)
	if errdefs.IsNotFound(err) {
		shim, shimErr := m.shimManager.shims.Get(ctx, id)
		if shimErr != nil {
			return nil, shimErr
		}
		return newShimTask(shim)
	}
	return t, err
}

// Tasks lists all tasks
func (m *TaskManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	var tasks []runtime.Task
	sts, err := m.sandboxedTaskManager.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, sts...)

	shims, err := m.shimManager.shims.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}

	for _, s := range shims {
		st, err := newShimTask(s)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, st)
	}
	return tasks, nil
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	exit, err := m.sandboxedTaskManager.Delete(ctx, taskID)
	if err == nil {
		return exit, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, err
	}

	shim, err := m.shimManager.shims.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	exit, err = shimTask.delete(ctx, func(ctx context.Context, id string) {
		m.shimManager.shims.Delete(ctx, id)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return exit, nil
}

func (m *TaskManager) loadExistingTasks(ctx context.Context) error {
	nsDirs, err := os.ReadDir(m.state)
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
		log.G(ctx).WithField("namespace", ns).Debug("loading tasks in namespace")
		if err := m.loadTasks(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("loading tasks in namespace")
			continue
		}
		if err := m.cleanupWorkDirs(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("cleanup working directory in namespace")
			continue
		}
	}
	return nil
}

func (m *TaskManager) loadTasks(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("namespace", ns))

	bundles, err := os.ReadDir(filepath.Join(m.state, ns))
	if err != nil {
		return err
	}
	for _, sd := range bundles {
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
		f, err := os.Open(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}

		bf, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}

		// Read sandbox ID this task belongs to.
		// it will return ErrorNotExit if it is not a sandboxed task
		sbID, err := os.ReadFile(filepath.Join(bundle.Path, "sandbox"))
		if err == nil {
			loadErr := m.sandboxedTaskManager.Load(ctx, string(sbID), bundle)
			if loadErr != nil && loadErr != ErrCanNotHandle {
				log.G(ctx).WithError(loadErr).Errorf("failed to load %s", bundle.Path)
				bundle.Delete()
			}
			// if loadErr == ErrCanNotHandle, should fallback to the shim load
			if loadErr == nil {
				continue
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			bundle.Delete()
			continue
		}

		if err := m.shimManager.Load(ctx, bundle); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to load shim %s", bundle.Path)
			bundle.Delete()
			continue
		}
	}
	return nil
}

func (m *TaskManager) cleanupWorkDirs(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	f, err := os.Open(filepath.Join(m.root, ns))
	if err != nil {
		return err
	}
	defer f.Close()

	dirs, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, dir := range dirs {
		// if the task was not loaded, cleanup and empty working directory
		// this can happen on a reboot where /run for the bundle state is cleaned up
		// but that persistent working dir is left
		if _, err := m.Get(ctx, dir); err != nil {
			path := filepath.Join(m.root, ns, dir)
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
	}
	return nil
}

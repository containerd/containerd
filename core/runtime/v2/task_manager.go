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

	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

// TaskConfig for the runtime task manager
type TaskConfig struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.RuntimePluginV2,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.ShimPlugin,
			plugins.SandboxControllerPlugin,
		},
		Config: &TaskConfig{
			Platforms: defaultPlatforms(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*TaskConfig)

			supportedPlatforms, err := platforms.ParseAll(config.Platforms)
			if err != nil {
				return nil, err
			}
			ic.Meta.Platforms = supportedPlatforms

			shimManagerI, err := ic.GetSingle(plugins.ShimPlugin)
			if err != nil {
				return nil, err
			}
			shimManager := shimManagerI.(*ShimManager)

			sandboxedTaskManager, err := NewSandboxedTaskManager(ic)
			if err != nil {
				return nil, err
			}
			shimedTaskManager := &ShimTaskManager{
				shimManager: shimManager,
			}

			root, state := ic.Properties[plugins.PropertyRootDir], ic.Properties[plugins.PropertyStateDir]
			for _, d := range []string{root, state} {
				if err := os.MkdirAll(d, 0711); err != nil {
					return nil, err
				}
			}

			return NewTaskManager(ic.Context, root, state, shimedTaskManager, sandboxedTaskManager)
		},
	})
}

// TaskManager wraps task service client on top of ShimTaskManager or SandboxedTaskManager.
type TaskManager struct {
	root                 string
	state                string
	shimTaskManager      *ShimTaskManager
	sandboxedTaskManager *SandboxedTaskManager
}

// NewTaskManager creates a new task TaskManager instance.
// root is the rootDir of TaskManager plugin to store persistent data
// state is the stateDir of TaskManager plugin to store transient data
// shims is  ShimManager for TaskManager to create/delete shims
func NewTaskManager(ctx context.Context,
	root, state string,
	shimTaskManager *ShimTaskManager,
	sandboxedTaskManager *SandboxedTaskManager) (*TaskManager, error) {

	m := &TaskManager{
		root:                 root,
		state:                state,
		shimTaskManager:      shimTaskManager,
		sandboxedTaskManager: sandboxedTaskManager,
	}
	if err := m.loadExistingTasks(ctx, state, root); err != nil {
		return nil, fmt.Errorf("failed to load existing shims for task manager")
	}
	return m, nil
}

// ID of the task shimTaskManager
func (m *TaskManager) ID() string {
	return plugins.RuntimePluginV2.String() + ".task"
}

// Create launches new shim instance and creates new task
func (m *TaskManager) Create(ctx context.Context, taskID string, opts runtime.CreateOpts) (_ runtime.Task, retErr error) {
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
	return m.shimTaskManager.Create(ctx, taskID, bundle, opts)
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	t, err := m.sandboxedTaskManager.Get(ctx, id)
	if errdefs.IsNotFound(err) {
		t, err = m.shimTaskManager.Get(ctx, id)
		if err != nil {
			return nil, err
		}
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
	shimedTasks, err := m.shimTaskManager.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, shimedTasks...)
	return tasks, nil
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	exit, err := m.sandboxedTaskManager.Delete(ctx, taskID)
	if !errdefs.IsNotFound(err) {
		return exit, err
	}
	return m.shimTaskManager.Delete(ctx, taskID)
}

func (m *TaskManager) loadExistingTasks(ctx context.Context, stateDir string, rootDir string) error {
	if err := TraversBundles(ctx, stateDir, func(ctx context.Context, bundle *Bundle) error {
		// Read sandbox ID this task belongs to.
		// it will return ErrorNotExit if it is not a sandboxed task
		sbID, err := os.ReadFile(filepath.Join(bundle.Path, "sandbox"))
		if err == nil {
			loadErr := m.sandboxedTaskManager.Load(ctx, string(sbID), bundle)
			if loadErr != nil && loadErr != ErrCanNotHandle {
				log.G(ctx).WithError(loadErr).Errorf("failed to load %s", bundle.Path)
			}
			// if loadErr == ErrCanNotHandle, should fallback to the shim load
			if loadErr == nil {
				return nil
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			bundle.Delete()
			return err
		}

		if err := m.shimTaskManager.Load(ctx, bundle); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to load shim %s", bundle.Path)
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if err := TraversWorkDirs(ctx, rootDir, func(ctx context.Context, ns, dir string) error {
		if _, err := m.Get(ctx, dir); err != nil {
			path := filepath.Join(rootDir, ns, dir)
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

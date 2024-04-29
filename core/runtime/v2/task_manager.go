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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	apitypes "github.com/containerd/containerd/v2/api/types"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/internal/cleanup"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf/proto"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.RuntimePluginV2,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.ShimPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimManagerI, err := ic.GetByID(plugins.ShimPlugin, "shim")
			if err != nil {
				return nil, err
			}
			shimManager := shimManagerI.(*ShimManager)
			root, state := ic.Properties[plugins.PropertyRootDir], ic.Properties[plugins.PropertyStateDir]
			for _, d := range []string{root, state} {
				if err := os.MkdirAll(d, 0711); err != nil {
					return nil, err
				}
			}
			return NewTaskManager(ic.Context, root, state, shimManager)
		},
	})
}

// TaskManager wraps task service client on top of shim manager.
type TaskManager struct {
	root    string
	state   string
	manager *ShimManager
}

// NewTaskManager creates a new task manager instance.
// root is the rootDir of TaskManager plugin to store persistent data
// state is the stateDir of TaskManager plugin to store transient data
// shims is  ShimManager for TaskManager to create/delete shims
func NewTaskManager(ctx context.Context, root, state string, shims *ShimManager) (*TaskManager, error) {
	if err := shims.LoadExistingShims(ctx, state, root); err != nil {
		return nil, fmt.Errorf("failed to load existing shims for task manager")
	}
	m := &TaskManager{
		root:    root,
		state:   state,
		manager: shims,
	}
	return m, nil
}

// ID of the task manager
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

	shim, err := m.manager.Start(ctx, taskID, bundle, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start shim: %w", err)
	}

	// Cast to shim task and call task service to create a new container task instance.
	// This will not be required once shim service / client implemented.
	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	t, err := shimTask.Create(ctx, opts)
	if err != nil {
		// NOTE: ctx contains required namespace information.
		m.manager.shims.Delete(ctx, taskID)

		dctx, cancel := timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
		defer cancel()

		sandboxed := opts.SandboxID != ""
		_, errShim := shimTask.delete(dctx, sandboxed, func(context.Context, string) {})
		if errShim != nil {
			if errdefs.IsDeadlineExceeded(errShim) {
				dctx, cancel = timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
				defer cancel()
			}

			shimTask.Shutdown(dctx)
			shimTask.Close()
		}

		return nil, fmt.Errorf("failed to create shim task: %w", err)
	}

	return t, nil
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	shim, err := m.manager.shims.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return newShimTask(shim)
}

// Tasks lists all tasks
func (m *TaskManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	shims, err := m.manager.shims.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Task, len(shims))
	for i := range shims {
		newClient, err := newShimTask(shims[i])
		if err != nil {
			return nil, err
		}
		out[i] = newClient
	}
	return out, nil
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	shim, err := m.manager.shims.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	container, err := m.manager.containers.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	sandboxed := container.SandboxID != ""

	exit, err := shimTask.delete(ctx, sandboxed, func(ctx context.Context, id string) {
		m.manager.shims.Delete(ctx, id)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return exit, nil
}

func (m *TaskManager) PluginInfo(ctx context.Context, request interface{}) (interface{}, error) {
	req, ok := request.(*apitypes.RuntimeRequest)
	if !ok {
		return nil, fmt.Errorf("unknown request type %T: %w", request, errdefs.ErrNotImplemented)
	}

	runtimePath, err := m.manager.resolveRuntimePath(req.RuntimePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime path: %w", err)
	}
	var optsB []byte
	if req.Options != nil {
		optsB, err = proto.Marshal(req.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %s: %w", req.Options.TypeUrl, err)
		}
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, runtimePath, "-info")
	cmd.Stdin = bytes.NewReader(optsB)
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run %v: %w (stderr: %q)", cmd.Args, err, stderr.String())
	}
	var info apitypes.RuntimeInfo
	if err = proto.Unmarshal(stdout, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stdout from %v into %T: %w", cmd.Args, &info, err)
	}
	return &info, nil
}

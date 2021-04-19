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

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	proto "github.com/containerd/containerd/runtime/v2/task"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

// SandboxManager is a layer on top of task manager to support sandboxed environments.
// Container record now have SandboxID field to identify which sandbox the container belongs to.
// If it's empty, SandboxManager will fallback to TaskManager's implementation to handle container tasks.
// Otherwise sandbox manager will find proper shim instance and will route query to that shim.
type SandboxManager struct {
	tasks     *TaskManager
	sandboxes *runtime.TaskList
}

var _ runtime.SandboxRuntime = (*SandboxManager)(nil)
var _ runtime.PlatformRuntime = (*SandboxManager)(nil)

// NewSandboxManager will create a sandbox manager to control sandboxes on top of the existing v2 task manager.
func NewSandboxManager(tasks *TaskManager) *SandboxManager {
	return &SandboxManager{
		tasks:     tasks,
		sandboxes: runtime.NewTaskList(),
	}
}

// Start will launch a new sandbox instance task.
func (m *SandboxManager) Start(ctx context.Context, sandboxID string, opts runtime.SandboxOpts) (_ runtime.Process, retErr error) {
	if _, err := m.getShim(ctx, sandboxID); err == nil {
		return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "sandbox %s already running", sandboxID)
	}

	bundle, err := NewBundle(ctx, m.tasks.root, m.tasks.state, sandboxID, opts.Spec.Value)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create bundle")
	}

	shim, err := m.tasks.startShim(ctx, bundle, opts.RuntimeName, opts.RuntimeOpts, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to start shim")
	}

	svc, err := shim.Sandbox(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve sandbox service from shim")
	}

	if _, err := svc.StartSandbox(ctx, &proto.StartSandboxRequest{
		SandboxID:  sandboxID,
		BundlePath: bundle.Path,
	}); err != nil {
		return nil, errors.Wrap(err, "failed to start sandbox instance")
	}

	if err := m.sandboxes.Add(ctx, shim); err != nil {
		return nil, err
	}

	return shim, nil
}

// Shutdown will turn down existing sandbox instance
func (m *SandboxManager) Shutdown(ctx context.Context, sandboxID string) error {
	shim, err := m.getShim(ctx, sandboxID)
	if err != nil {
		return err
	}

	svc, err := shim.Sandbox(ctx)
	if err != nil {
		return err
	}

	if _, err := svc.StopSandbox(ctx, &proto.StopSandboxRequest{
		SandboxID: sandboxID,
	}); err != nil {
		return errors.Wrapf(err, "failed to stop %s", sandboxID)
	}

	m.tasks.deleteShim(shim)
	return nil
}

// Pause will freeze running sandbox instance
func (m *SandboxManager) Pause(ctx context.Context, sandboxID string) error {
	svc, err := m.getSvc(ctx, sandboxID)
	if err != nil {
		return err
	}

	if _, err := svc.PauseSandbox(ctx, &proto.PauseSandboxRequest{
		SandboxID: sandboxID,
	}); err != nil {
		return errors.Wrapf(err, "failed to pause sandbox %s", sandboxID)
	}

	return nil
}

// Resume will unfreeze previously paused sandbox instance
func (m *SandboxManager) Resume(ctx context.Context, sandboxID string) error {
	svc, err := m.getSvc(ctx, sandboxID)
	if err != nil {
		return err
	}

	if _, err := svc.ResumeSandbox(ctx, &proto.ResumeSandboxRequest{
		SandboxID: sandboxID,
	}); err != nil {
		return errors.Wrapf(err, "failed to resume sandbox %s", sandboxID)
	}

	return nil
}

// Ping is a lightweight shim call to check whether sandbox alive
func (m *SandboxManager) Ping(ctx context.Context, sandboxID string) error {
	svc, err := m.getSvc(ctx, sandboxID)
	if err != nil {
		return err
	}

	if _, err := svc.PingSandbox(ctx, &proto.PingRequest{
		SandboxID: sandboxID,
	}); err != nil {
		return err
	}

	return nil
}

// Status will return current status of the running sandbox instance
func (m *SandboxManager) Status(ctx context.Context, sandboxID string) (*types.Any, error) {
	svc, err := m.getSvc(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	status, err := svc.SandboxStatus(ctx, &proto.SandboxStatusRequest{
		SandboxID: sandboxID,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query sandbox %s status", sandboxID)
	}

	return status.Status, nil
}

// ID of the sandbox runtime
func (m *SandboxManager) ID() string {
	return fmt.Sprintf("%s.%s", plugin.RuntimePluginV2, "sandbox")
}

// Create creates a task with the provided id and options.
func (m *SandboxManager) Create(ctx context.Context, id string, opts runtime.CreateOpts) (runtime.Task, error) {
	container, err := m.tasks.containers.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// No sandbox, fallback to task manager
	if container.SandboxID == "" {
		return m.tasks.Create(ctx, id, opts)
	}

	// Find sandbox this task belongs to
	sandbox, err := m.getShim(ctx, container.SandboxID)
	if err != nil {
		return nil, err
	}

	task, err := sandbox.Create(ctx, opts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create sandbox task")
	}

	if err := m.tasks.Add(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

// Get returns a task.
func (m *SandboxManager) Get(ctx context.Context, taskID string) (runtime.Task, error) {
	return m.tasks.Get(ctx, taskID)
}

// Tasks returns all the current tasks for the runtime.
func (m *SandboxManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	return m.tasks.Tasks(ctx, all)
}

// Add adds a task into runtime.
func (m *SandboxManager) Add(ctx context.Context, task runtime.Task) error {
	return m.tasks.Add(ctx, task)
}

// Delete remove a task.
func (m *SandboxManager) Delete(ctx context.Context, taskID string) {
	m.tasks.Delete(ctx, taskID)
}

// Find will return a sandbox instance by identifier
func (m *SandboxManager) Find(ctx context.Context, sandboxID string) (runtime.Process, error) {
	return m.getShim(ctx, sandboxID)
}

func (m *SandboxManager) getShim(ctx context.Context, sandboxID string) (*shim, error) {
	inst, err := m.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get sandbox with ID %s", sandboxID)
	}

	return inst.(*shim), nil
}

func (m *SandboxManager) getSvc(ctx context.Context, sandboxID string) (proto.SandboxService, error) {
	shim, err := m.getShim(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	service, err := shim.Sandbox(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve sandbox service")
	}

	return service, nil
}

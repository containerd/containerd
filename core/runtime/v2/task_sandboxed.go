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
	"strings"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/ttrpc"

	"github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	shimclient "github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/plugins"
)

var ErrCanNotHandle = errors.New("can not handle this task")

type SandboxedTaskManager struct {
	sandboxStore       sandbox.Store
	sandboxControllers map[string]sandbox.Controller
	tasks              *runtime.NSMap[*sandboxedTask]
}

func NewSandboxedTaskManager(ic *plugin.InitContext) (*SandboxedTaskManager, error) {
	m, err := ic.GetSingle(plugins.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	sandboxStore := metadata.NewSandboxStore(m.(*metadata.DB))

	scs, err := ic.GetByType(plugins.SandboxControllerPluginV2)
	if err != nil {
		return nil, err
	}
	sandboxControllers := make(map[string]sandbox.Controller)
	for name, p := range scs {
		sandboxControllers[name] = p.(sandbox.Controller)
	}

	return &SandboxedTaskManager{
		sandboxStore:       sandboxStore,
		sandboxControllers: sandboxControllers,
		tasks:              runtime.NewNSMap[*sandboxedTask](),
	}, nil
}

func (s *SandboxedTaskManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (_ runtime.Task, retErr error) {
	if len(opts.SandboxID) == 0 {
		return nil, fmt.Errorf("no sandbox id specified for task %s", taskID)
	}
	sb, err := s.loadSandboxV2(ctx, opts.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := namespaces.NamespaceRequired(ctx); err != nil {
		return nil, err
	}

	// Write sandbox ID this task belongs to.
	if err := os.WriteFile(filepath.Join(bundle.Path, "sandbox"), []byte(opts.SandboxID), 0600); err != nil {
		return nil, err
	}

	if opts.Address == "" {
		return nil, fmt.Errorf("address of task api should not be empty for sandboxed task")
	}

	// The address returned from sandbox controller should be in the form like ttrpc+unix://<uds-path>
	// or grpc+vsock://<cid>:<port>, we should get the protocol from the url first.
	protocol, address, ok := strings.Cut(opts.Address, "+")
	if !ok {
		return nil, fmt.Errorf("the scheme of sandbox address should be in " +
			" the form of <protocol>+<unix|vsock|tcp>, i.e. ttrpc+unix or grpc+vsock")
	}
	params := shimclient.BootstrapParams{
		Version:  int(opts.Version),
		Protocol: protocol,
		Address:  address,
	}

	// Save bootstrap configuration (so containerd can restore shims after restart).
	if err := writeBootstrapParams(filepath.Join(bundle.Path, "bootstrap.json"), params); err != nil {
		return nil, fmt.Errorf("failed to write bootstrap.json: %w", err)
	}

	sbController, ok := s.sandboxControllers[sb.Sandboxer]
	if !ok {
		return nil, fmt.Errorf("failed to get sandbox controller by %s", sb.Sandboxer)
	}

	sandboxedTask, err := newSandboxedTask(ctx, sb, sbController, taskID, bundle, params)

	if err != nil {
		return nil, fmt.Errorf("failed to new sandboxed task: %w", err)
	}
	t, err := sandboxedTask.create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandboxed task: %w", err)
	}
	defer func() {
		if retErr != nil {
			if _, removeErr := sandboxedTask.delete(ctx); removeErr != nil {
				log.G(ctx).WithError(err).Warnf("failed to remove sandboxed task %s when create failed", taskID)
			}
		}
	}()
	if err := s.tasks.Add(ctx, sandboxedTask); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *SandboxedTaskManager) Load(ctx context.Context, sandboxID string, bundle *Bundle) error {
	sb, err := s.loadSandboxV2(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}

	filePath := filepath.Join(bundle.Path, "bootstrap.json")
	params, err := readBootstrapParams(filePath)
	if err != nil {
		return fmt.Errorf("failed to restore connection parameter from %s: %w", filePath, err)
	}

	sbController, ok := s.sandboxControllers[sb.Sandboxer]
	if !ok {
		return fmt.Errorf("failed to get sandbox controller by %s", sb.Sandboxer)
	}

	sandboxedTask, err := newSandboxedTask(ctx, sb, sbController, bundle.ID, bundle, params)
	if err != nil {
		return fmt.Errorf("failed to new sandboxed task: %w", err)
	}
	return s.tasks.Add(ctx, sandboxedTask)
}

func (s *SandboxedTaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	return s.tasks.Get(ctx, id)
}

func (s *SandboxedTaskManager) GetAll(ctx context.Context, all bool) ([]runtime.Task, error) {
	var tasks []runtime.Task
	ts, err := s.tasks.GetAll(ctx, all)
	if err != nil {
		return tasks, err
	}
	for _, t := range ts {
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *SandboxedTaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	st, err := s.tasks.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	exit, err := st.delete(ctx)
	if err != nil {
		if err == errdefs.ErrNotFound {
			s.tasks.Delete(ctx, taskID)
		}
		return nil, err
	}
	s.tasks.Delete(ctx, taskID)
	return exit, nil
}

// loadSandboxV2 loads sandboxes created by sandbox controller v2,
// so the pause container and the legacy pod sandbox is excluded.
func (s *SandboxedTaskManager) loadSandboxV2(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	sb, err := s.sandboxStore.Get(ctx, sandboxID)
	if err != nil {
		// If the sandbox is created in a previous version,
		// there is a possibility that it is stored in db as a pause container rather than a sandbox,
		// so we can only return ErrCanNotHandle here so that TaskManager can fallback to the legacy logic.
		if errdefs.IsNotFound(err) {
			return sandbox.Sandbox{}, ErrCanNotHandle
		}
		return sandbox.Sandbox{}, fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}
	if sb.Sandboxer == "podsandbox" {
		return sandbox.Sandbox{}, ErrCanNotHandle
	}
	return sb, nil
}

func newSandboxedTask(
	ctx context.Context,
	sandbox sandbox.Sandbox,
	sbController sandbox.Controller,
	taskID string,
	bundle *Bundle,
	params shimclient.BootstrapParams) (*sandboxedTask, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := makeConnection(ctx, taskID, params, func() {})

	if err != nil {
		return nil, fmt.Errorf("can not connect %v: %w", params, err)
	}

	taskClient, err := NewTaskClient(conn, params.Version)
	if err != nil {
		return nil, err
	}
	t := &sandboxedTask{
		namespace:         ns,
		sandbox:           sandbox,
		sandboxController: sbController,
		remoteTask: &remoteTask{
			id:     taskID,
			client: taskClient,
		},
		bundle: bundle,
	}

	return t, nil
}

// sandboxedTask wraped task running in a sandbox.
type sandboxedTask struct {
	namespace         string
	sandbox           sandbox.Sandbox
	sandboxController sandbox.Controller
	bundle            *Bundle
	*remoteTask
}

func (s *sandboxedTask) ID() string {
	return s.remoteTask.id
}

func (s *sandboxedTask) Namespace() string {
	return s.namespace
}

func (s *sandboxedTask) create(ctx context.Context, opts runtime.CreateOpts) (_ runtime.Task, retErr error) {
	// Update the resource in sandbox first
	var rootfs []*types.Mount
	for _, m := range opts.Rootfs {
		rootfs = append(rootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Target:  m.Target,
			Options: m.Options,
		})
	}

	if err := s.updateSandboxResource(ctx, types.ResourceOp_ADD, &types.TaskResource{
		TaskID: s.ID(),
		Spec:   protobuf.FromAny(opts.Spec),
		Rootfs: rootfs,
		Stdin:  opts.IO.Stdin,
		Stdout: opts.IO.Stdout,
		Stderr: opts.IO.Stderr,
	}); err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			if err := s.updateSandboxResource(ctx, types.ResourceOp_REMOVE, &types.TaskResource{
				TaskID: s.ID(),
			}); err != nil {
				log.G(ctx).Warnf("failed to remove task resource %s in sandbox %s: %v", s.ID(), s.sandbox.ID, err)
			}
		}
	}()
	// Then call Task Create api in sandbox to create the task inside sandbox
	if err := s.remoteTask.Create(ctx, s.bundle.Path, opts); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *sandboxedTask) delete(ctx context.Context) (*runtime.Exit, error) {
	resp, taskErr := s.client.Delete(ctx, &task.DeleteRequest{
		ID: s.ID(),
	})
	if taskErr != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(taskErr).Debug("failed to delete task")
		if !errors.Is(taskErr, ttrpc.ErrClosed) {
			taskErr = errdefs.FromGRPC(taskErr)
			if !errdefs.IsNotFound(taskErr) {
				return nil, taskErr
			}
		}
	}
	err := s.updateSandboxResource(ctx,
		types.ResourceOp_REMOVE,
		&types.TaskResource{TaskID: s.ID()})
	if err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Debug("failed to remove task resource from sandbox")
		// also ignore not found error here, maybe the resource is already removed from the sandbox
		if !errdefs.IsNotFound(err) {
			return nil, err
		}
	}

	if err := s.bundle.Delete(); err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to delete bundle")
	}

	if taskErr != nil {
		return nil, errdefs.ErrNotFound
	}
	return &runtime.Exit{
		Status:    resp.ExitStatus,
		Timestamp: protobuf.FromTimestamp(resp.ExitedAt),
		Pid:       resp.Pid,
	}, nil
}

func (s *sandboxedTask) Update(ctx context.Context, resources *ptypes.Any, annotations map[string]string) error {
	// Update resources in sandbox firstly
	err := s.updateSandboxResource(ctx, types.ResourceOp_UPDATE, &types.TaskResource{
		TaskID: s.ID(),
		Spec:   resources,
	})
	if err != nil {
		return err
	}

	// then call Update of task API
	err = s.remoteTask.Update(ctx, resources, annotations)
	if err != nil {
		// TODO: note that we do not rollback of the resource change in sandbox.
		// if we want to do it, we have to get the resource from the spec file in bundle
		// and call UpdateResource to change it back,
		// also we have to update the resources in the spec file in bundle after Update call succeed.
		return err
	}

	return nil
}

func (s *sandboxedTask) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (_ runtime.ExecProcess, retErr error) {
	// Update resources in sandbox firstly
	err := s.updateSandboxResource(ctx, types.ResourceOp_ADD, &types.TaskResource{
		TaskID: s.ID(),
		ExecID: id,
		Spec:   opts.Spec,
		Stdin:  opts.IO.Stdin,
		Stdout: opts.IO.Stdout,
		Stderr: opts.IO.Stderr,
	})
	if err != nil {
		return nil, err
	}

	defer func() {
		if retErr != nil {
			removeErr := s.updateSandboxResource(ctx, types.ResourceOp_REMOVE, &types.TaskResource{
				TaskID: s.ID(),
				ExecID: id,
			})
			if removeErr != nil {
				log.G(ctx).Warnf("failed to remove exec task resource %s %s in sandbox %s: %v", s.ID(), id, s.sandbox.ID, removeErr)
			}
		}
	}()

	p, err := s.remoteTask.Exec(ctx, id, opts)
	if err != nil {
		return nil, err
	}
	return &sandboxedProcess{
		ExecProcess: p,
		sbTask:      s,
	}, nil
}

func (s *sandboxedTask) updateSandboxResource(ctx context.Context, op types.ResourceOp, resource *types.TaskResource) error {
	return s.sandboxController.UpdateResource(ctx, s.sandbox.ID, op, resource)
}

type sandboxedProcess struct {
	runtime.ExecProcess
	sbTask *sandboxedTask
}

func (p *sandboxedProcess) Delete(ctx context.Context) (*runtime.Exit, error) {
	exit, err := p.ExecProcess.Delete(ctx)
	if err != nil {
		return nil, err
	}
	err = p.sbTask.updateSandboxResource(ctx, types.ResourceOp_REMOVE, &types.TaskResource{
		TaskID: p.sbTask.ID(),
		ExecID: p.ID(),
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return exit, nil
}

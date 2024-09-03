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

	"github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	shimclient "github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/ttrpc"
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

	scs, err := ic.GetByType(plugins.SandboxControllerPlugin)
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

func (s *SandboxedTaskManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (runtime.Task, error) {
	if len(opts.SandboxID) == 0 {
		return nil, fmt.Errorf("no sandbox id specified for task %s", taskID)
	}
	sb, err := s.loadSandbox(ctx, opts.SandboxID)
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

	sandboxedTask, err := newSandboxedTask(ctx, sb, taskID, bundle, params)
	if err != nil {
		return nil, fmt.Errorf("failed to new sandboxed task: %w", err)
	}
	err = sandboxedTask.Create(ctx, bundle.Path, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandboxed task: %w", err)
	}
	s.tasks.Add(ctx, sandboxedTask)
	return sandboxedTask, nil
}

func (s *SandboxedTaskManager) Load(ctx context.Context, sandboxID string, bundle *Bundle) error {
	sb, err := s.loadSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}

	filePath := filepath.Join(bundle.Path, "bootstrap.json")
	params, err := readBootstrapParams(filePath)
	if err != nil {
		return fmt.Errorf("failed to restore connection parameter from %s: %w", filePath, err)
	}

	sandboxedTask, err := newSandboxedTask(ctx, sb, bundle.ID, bundle, params)
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

	resp, taskErr := st.client.Delete(ctx, &task.DeleteRequest{
		ID: taskID,
	})
	if taskErr != nil {
		log.G(ctx).WithField("id", taskID).WithError(taskErr).Debug("failed to delete task")
		if !errors.Is(taskErr, ttrpc.ErrClosed) {
			taskErr = errdefs.FromGRPC(taskErr)
			if !errdefs.IsNotFound(taskErr) {
				return nil, taskErr
			}
		}
	}

	if err := st.bundle.Delete(); err != nil {
		log.G(ctx).WithField("id", taskID).WithError(err).Error("failed to delete bundle")
	}

	s.tasks.Delete(ctx, taskID)

	if taskErr != nil {
		return nil, errdefs.ErrNotFound
	}
	return &runtime.Exit{
		Status:    resp.ExitStatus,
		Timestamp: protobuf.FromTimestamp(resp.ExitedAt),
		Pid:       resp.Pid,
	}, nil
}

// loadSandbox loads sandboxes created by sandbox controller,
// so the pause container and the podsandbox is excluded.
func (s *SandboxedTaskManager) loadSandbox(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
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
		namespace: ns,
		sandbox:   sandbox,
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
	namespace string
	sandbox   sandbox.Sandbox
	bundle    *Bundle
	*remoteTask
}

func (s *sandboxedTask) ID() string {
	return s.remoteTask.id
}

func (s *sandboxedTask) Namespace() string {
	return s.namespace
}

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

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/ttrpc"

	"github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/runtime"
	shimclient "github.com/containerd/containerd/v2/core/runtime/v2/shim"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
)

var ErrCanNotHandle = errors.New("can not handle this task")

type SandboxedTaskManager struct {
	client *client.Client
	tasks  *runtime.NSMap[*sandboxedTask]
}

func NewSandboxedTaskManager(ic *plugin.InitContext) (*SandboxedTaskManager, error) {
	m, err := ic.GetSingle(plugins.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	ss := metadata.NewSandboxStore(m.(*metadata.DB))

	sandboxers, err := ic.GetByType(plugins.SandboxControllerPluginV2)
	if err != nil {
		return nil, err
	}
	sc := make(map[string]sandbox.Controller)
	for name, p := range sandboxers {
		sc[name] = p.(sandbox.Controller)
	}

	c, err := client.New(
		"",
		client.WithServices(client.WithSandboxStore(ss), client.WithSandboxControllers(sc)),
	)
	if err != nil {
		return nil, err
	}
	return &SandboxedTaskManager{
		client: c,
		tasks:  runtime.NewNSMap[*sandboxedTask](),
	}, nil
}

func (s *SandboxedTaskManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (runtime.Task, error) {
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

	sandboxedTask, err := newSandboxedTask(ctx, sb, taskID, bundle)
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
	sb, err := s.loadSandboxV2(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}

	sandboxedTask, err := newSandboxedTask(ctx, sb, bundle.ID, bundle)
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

// loadSandboxV2 loads sandboxes created by sandbox controller v2,
// so the pause container and the legacy pod sandbox is excluded.
func (s *SandboxedTaskManager) loadSandboxV2(ctx context.Context, sandboxID string) (client.Sandbox, error) {
	sb, err := s.client.LoadSandbox(ctx, sandboxID)
	if err != nil {
		// If the sandbox is created in a previous version,
		// there is a possibility that it is stored in db as a pause container rather than a sandbox,
		// so we can only return ErrCanNotHandle here so that TaskManager can fallback to the legacy logic.
		if errdefs.IsNotFound(err) {
			return nil, ErrCanNotHandle
		}
		return nil, fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}
	if sb.Metadata().Sandboxer == "podsandbox" {
		return nil, ErrCanNotHandle
	}
	return sb, nil
}

func newSandboxedTask(ctx context.Context, sandbox client.Sandbox, taskID string, bundle *Bundle) (*sandboxedTask, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := sandbox.Endpoint()
	if len(endpoint.Address) == 0 {
		return nil, fmt.Errorf("sandbox %v has no address", sandbox.ID())
	}

	conn, err := makeConnection(ctx, taskID, shimclient.BootstrapParams{
		Version:  endpoint.Version,
		Address:  endpoint.Address,
		Protocol: endpoint.Protocol,
	}, func() {})

	if err != nil {
		return nil, fmt.Errorf("can not connect %v: %w", endpoint, err)
	}

	taskClient, err := NewTaskClient(conn, endpoint.Version)
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
	sandbox   client.Sandbox
	bundle    *Bundle
	*remoteTask
}

func (s *sandboxedTask) ID() string {
	return s.remoteTask.id
}

func (s *sandboxedTask) Namespace() string {
	return s.namespace
}

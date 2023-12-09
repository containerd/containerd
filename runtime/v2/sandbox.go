package v2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/log"
	"github.com/containerd/ttrpc"

	"github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/protobuf/types"
	"github.com/containerd/containerd/v2/runtime"
	shim2 "github.com/containerd/containerd/v2/runtime/v2/shim"
)

var ErrCanNotHandle = errors.New("can not handle this task")

type SandboxedTaskManager struct {
	client *client.Client
	tasks  *runtime.NSMap[*sandboxedTask]
}

func NewSandboxedTaskManager(c *client.Client) *SandboxedTaskManager {
	return &SandboxedTaskManager{
		client: c,
		tasks:  runtime.NewNSMap[*sandboxedTask](),
	}
}

func (s *SandboxedTaskManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (runtime.Task, error) {
	if len(opts.SandboxID) == 0 {
		return nil, fmt.Errorf("no sandbox id specified for task %s", taskID)
	}

	sb, err := s.client.LoadSandbox(ctx, opts.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox %s: %w", opts.SandboxID, err)
	}

	if _, ok := s.client.InMemorySandboxController(sb.Metadata().Sandboxer); !ok {
		return nil, ErrCanNotHandle
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
	t, err := sandboxedTask.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandboxed task: %w", err)
	}
	s.tasks.Add(ctx, sandboxedTask)
	return t, nil
}

func (s *SandboxedTaskManager) Load(ctx context.Context, sandboxID string, bundle *Bundle) error {
	sb, err := s.client.LoadSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s: %w", sandboxID, err)
	}
	if _, ok := s.client.InMemorySandboxController(sb.Metadata().Sandboxer); !ok {
		return ErrCanNotHandle
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

	resp, taskErr := st.taskClient.Delete(ctx, &task.DeleteRequest{
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
	err = st.sandbox.RemoveTask(ctx, taskID)
	if err != nil {
		log.G(ctx).WithField("id", taskID).WithError(err).Debug("failed to remote task from sandbox")
		if !errdefs.IsNotFound(err) {
			return nil, err
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

func newSandboxedTask(ctx context.Context, sandbox client.Sandbox, taskID string, bundle *Bundle) (*sandboxedTask, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	address := sandbox.Metadata().Address
	if len(address) == 0 {
		return nil, fmt.Errorf("sandbox %v has no address", sandbox.ID())
	}

	conn, err := shim2.Connect(address, shim2.AnonReconnectDialer)
	if err != nil {
		return nil, fmt.Errorf("can not connect %v: %w", address, err)
	}

	taskClient, err := NewTaskClient(ttrpc.NewClient(conn), 3)
	if err != nil {
		return nil, err
	}
	t := &sandboxedTask{
		namespace: ns,
		sandbox:   sandbox,
		remoteTask: &remoteTask{
			id:         taskID,
			taskClient: taskClient,
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

func (s *sandboxedTask) Create(ctx context.Context, opts runtime.CreateOpts) (runtime.Task, error) {
	err := s.sandbox.NewTask(ctx, s.ID(), opts.Spec, opts.Rootfs)
	if err != nil {
		return nil, err
	}
	if err := s.remoteTask.Create(ctx, s.bundle.Path, opts); err != nil {
		if removeErr := s.sandbox.RemoveTask(ctx, s.ID()); removeErr != nil {
			log.G(ctx).Warnf("failed to remove task %s in sandbox %s: %v", s.ID(), s.sandbox.ID(), removeErr)
		}
		return nil, err
	}
	return s, nil
}

func (s *sandboxedTask) ID() string {
	return s.remoteTask.id
}

func (s *sandboxedTask) Namespace() string {
	return s.namespace
}

// Update Override the Update in remoteTask to update the resource in sandbox first
func (s *sandboxedTask) Update(ctx context.Context, resources *types.Any, annotations map[string]string) error {
	// call Update of task in the sandbox first
	err := s.remoteTask.Update(ctx, resources, annotations)
	if err != nil {
		return err
	}

	// then call UpdateTask of sandbox to update the resources in the sandbox
	err = s.sandbox.UpdateTask(ctx, s.ID(), resources)
	if err != nil {
		return err
	}
	return nil
}

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
	"slices"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-spec/specs-go/features"

	"github.com/containerd/containerd/api/runtime/task/v3"
	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/internal/cleanup"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/pkg/timeout"
)

type ShimTaskManager struct {
	shimManager *ShimManager
}

func (m *ShimTaskManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (runtime.Task, error) {
	shim, err := m.shimManager.Start(ctx, taskID, bundle, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start shim: %w", err)
	}

	// Cast to shim task and call task service to create a new container task instance.
	// This will not be required once shim service / client implemented.
	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	// runc ignores silently features it doesn't know about, so for things that this is
	// problematic let's check if this runc version supports them.
	if err := m.validateRuntimeFeatures(ctx, opts); err != nil {
		return nil, fmt.Errorf("failed to validate OCI runtime features: %w", err)
	}

	err = shimTask.Create(ctx, bundle.Path, opts)
	if err != nil {
		// NOTE: ctx contains required namespace information.
		m.shimManager.shims.Delete(ctx, taskID)

		dctx, cancel := timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
		defer cancel()

		_, errShim := shimTask.delete(dctx, func(context.Context, string) {})
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

	return shimTask, nil
}

func (m *ShimTaskManager) Load(ctx context.Context, bundle *Bundle) error {
	return m.shimManager.loadShim(ctx, bundle, func(instance ShimInstance) error {
		s, err := newShimTask(instance)
		if err != nil {
			return err
		}
		ctx, cancel := timeout.WithContext(ctx, loadTimeout)
		defer cancel()
		if _, err := s.PID(ctx); err != nil {
			return err
		}

		// There are 2 possibilities for the loaded shim here:
		// 1. It could be a shim that is running a task.
		// 2. Or it could be a shim that was created for running a task but
		// something happened (probably a containerd crash) and the task was never
		// created. This shim process should be cleaned up here. Look at
		// containerd/containerd#6860 for further details.
		pInfo, pidErr := s.Pids(ctx)
		if len(pInfo) == 0 || errors.Is(pidErr, errdefs.ErrNotFound) {
			log.G(ctx).WithField("id", s.ID()).Info("cleaning leaked shim process")
			return err
		}

		return nil
	})
}

func (m *ShimTaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	shim, err := m.shimManager.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return newShimTask(shim)
}

func (m *ShimTaskManager) GetAll(ctx context.Context, all bool) ([]runtime.Task, error) {
	var tasks []runtime.Task
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

func (m *ShimTaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	shim, err := m.shimManager.shims.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	exit, err := shimTask.delete(ctx, func(ctx context.Context, id string) {
		m.shimManager.shims.Delete(ctx, id)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return exit, nil
}

var _ runtime.Task = &shimTask{}

// shimTask wraps shim process and adds task service client for compatibility with existing shim manager.
type shimTask struct {
	ShimInstance
	*remoteTask
}

func newShimTask(shim ShimInstance) (*shimTask, error) {
	_, version := shim.Endpoint()
	taskClient, err := NewTaskClient(shim.Client(), version)
	if err != nil {
		return nil, err
	}

	return &shimTask{
		ShimInstance: shim,
		remoteTask: &remoteTask{
			id:     shim.ID(),
			client: taskClient,
		},
	}, nil
}

func (s *shimTask) Shutdown(ctx context.Context) error {
	_, err := s.remoteTask.client.Shutdown(ctx, &task.ShutdownRequest{
		ID: s.ID(),
	})
	if err != nil && !errors.Is(err, ttrpc.ErrClosed) {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) waitShutdown(ctx context.Context) error {
	ctx, cancel := timeout.WithContext(ctx, shutdownTimeout)
	defer cancel()
	return s.Shutdown(ctx)
}

func (s *shimTask) delete(ctx context.Context, removeTask func(ctx context.Context, id string)) (*runtime.Exit, error) {
	response, shimErr := s.remoteTask.client.Delete(ctx, &task.DeleteRequest{
		ID: s.ID(),
	})
	if shimErr != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(shimErr).Debug("failed to delete task")
		if !errors.Is(shimErr, ttrpc.ErrClosed) {
			shimErr = errdefs.FromGRPC(shimErr)
			if !errdefs.IsNotFound(shimErr) {
				return nil, shimErr
			}
		}
	}

	// NOTE: If the shim has been killed and ttrpc connection has been
	// closed, the shimErr will not be nil. For this case, the event
	// subscriber, like moby/moby, might have received the exit or delete
	// events. Just in case, we should allow ttrpc-callback-on-close to
	// send the exit and delete events again. And the exit status will
	// depend on result of shimV2.Delete.
	//
	// If not, the shim has been delivered the exit and delete events.
	// So we should remove the record and prevent duplicate events from
	// ttrpc-callback-on-close.
	//
	// TODO: It's hard to guarantee that the event is unique and sent only
	// once. The moby/moby should not rely on that assumption that there is
	// only one exit event. The moby/moby should handle the duplicate events.
	//
	// REF: https://github.com/containerd/containerd/issues/4769
	if shimErr == nil {
		removeTask(ctx, s.ID())
	}

	if err := s.waitShutdown(ctx); err != nil {
		// FIXME(fuweid):
		//
		// If the error is context canceled, should we use context.TODO()
		// to wait for it?
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to shutdown shim task and the shim might be leaked")
	}

	if err := s.ShimInstance.Delete(ctx); err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to delete shim")
	}

	// remove self from the runtime task list
	// this seems dirty but it cleans up the API across runtimes, tasks, and the service
	removeTask(ctx, s.ID())

	if shimErr != nil {
		return nil, shimErr
	}

	return &runtime.Exit{
		Status:    response.ExitStatus,
		Timestamp: protobuf.FromTimestamp(response.ExitedAt),
		Pid:       response.Pid,
	}, nil
}

func (m *ShimTaskManager) validateRuntimeFeatures(ctx context.Context, opts runtime.CreateOpts) error {
	var spec specs.Spec
	if err := typeurl.UnmarshalTo(opts.Spec, &spec); err != nil {
		return fmt.Errorf("unmarshal spec: %w", err)
	}

	// Only ask for the PluginInfo if idmap mounts are used.
	if !usesIDMapMounts(spec) {
		return nil
	}

	topts := opts.TaskOptions
	if topts == nil || topts.GetValue() == nil {
		topts = opts.RuntimeOptions
	}

	pInfo, err := m.shimManager.PluginInfo(ctx, &apitypes.RuntimeRequest{
		RuntimePath: opts.Runtime,
		Options:     typeurl.MarshalProto(topts),
	})
	if err != nil {
		return fmt.Errorf("runtime info: %w", err)
	}

	pluginInfo, ok := pInfo.(*apitypes.RuntimeInfo)
	if !ok {
		return fmt.Errorf("invalid runtime info type: %T", pInfo)
	}

	feat, err := typeurl.UnmarshalAny(pluginInfo.Features)
	if err != nil {
		return fmt.Errorf("unmarshal runtime features: %w", err)
	}

	// runc-compatible runtimes silently ignores features it doesn't know about. But ignoring
	// our request to use idmap mounts can break permissions in the volume, so let's make sure
	// it supports it. For more info, see:
	//	https://github.com/opencontainers/runtime-spec/pull/1219
	//
	features, ok := feat.(*features.Features)
	if !ok {
		// Leave alone non runc-compatible runtimes that don't provide the features info,
		// they might not be affected by this.
		return nil
	}

	if err := supportsIDMapMounts(features); err != nil {
		return fmt.Errorf("idmap mounts not supported: %w", err)
	}

	return nil
}

func usesIDMapMounts(spec specs.Spec) bool {
	for _, m := range spec.Mounts {
		if m.UIDMappings != nil || m.GIDMappings != nil {
			return true
		}
		if slices.Contains(m.Options, "idmap") || slices.Contains(m.Options, "ridmap") {
			return true
		}

	}
	return false
}

func supportsIDMapMounts(features *features.Features) error {
	if features.Linux.MountExtensions == nil || features.Linux.MountExtensions.IDMap == nil {
		return errors.New("missing `mountExtensions.idmap` entry in `features` command")
	}
	if enabled := features.Linux.MountExtensions.IDMap.Enabled; enabled == nil || !*enabled {
		return errors.New("entry `mountExtensions.idmap.Enabled` in `features` command not present or disabled")
	}
	return nil
}

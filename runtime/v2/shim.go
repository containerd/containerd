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
	"io"
	"os"
	"path/filepath"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/timeout"
	"github.com/containerd/containerd/protobuf"
	ptypes "github.com/containerd/containerd/protobuf/types"
	"github.com/containerd/containerd/runtime"
	client "github.com/containerd/containerd/runtime/v2/shim"

	"github.com/containerd/ttrpc"
	"github.com/hashicorp/go-multierror"
	"github.com/sirupsen/logrus"
)

const (
	loadTimeout     = "io.containerd.timeout.shim.load"
	cleanupTimeout  = "io.containerd.timeout.shim.cleanup"
	shutdownTimeout = "io.containerd.timeout.shim.shutdown"
)

func init() {
	timeout.Set(loadTimeout, 5*time.Second)
	timeout.Set(cleanupTimeout, 5*time.Second)
	timeout.Set(shutdownTimeout, 3*time.Second)
}

func loadAddress(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func loadShim(ctx context.Context, bundle *Bundle, onClose func()) (_ ShimInstance, err error) {
	address, err := loadAddress(filepath.Join(bundle.Path, "address"))
	if err != nil {
		return nil, err
	}
	conn, err := client.Connect(address, client.AnonReconnectDialer)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	shimCtx, cancelShimLog := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancelShimLog()
		}
	}()
	f, err := openShimLog(shimCtx, bundle, client.AnonReconnectDialer)
	if err != nil {
		return nil, fmt.Errorf("open shim log pipe when reload: %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	// open the log pipe and block until the writer is ready
	// this helps with synchronization of the shim
	// copy the shim's logs to containerd's output
	go func() {
		defer f.Close()
		_, err := io.Copy(os.Stderr, f)
		// To prevent flood of error messages, the expected error
		// should be reset, like os.ErrClosed or os.ErrNotExist, which
		// depends on platform.
		err = checkCopyShimLogError(ctx, err)
		if err != nil {
			log.G(ctx).WithError(err).Error("copy shim log after reload")
		}
	}()
	onCloseWithShimLog := func() {
		onClose()
		cancelShimLog()
		f.Close()
	}
	client := ttrpc.NewClient(conn, ttrpc.WithOnClose(onCloseWithShimLog))
	defer func() {
		if err != nil {
			client.Close()
		}
	}()
	shim := &shim{
		bundle: bundle,
		client: client,
	}
	ctx, cancel := timeout.WithContext(ctx, loadTimeout)
	defer cancel()
	// Check connectivity, TaskService is the only required service, so create a temp one to check connection.
	s := newShimTask(shim)
	if _, err := s.PID(ctx); err != nil {
		return nil, err
	}
	return shim, nil
}

func cleanupAfterDeadShim(ctx context.Context, id, ns string, rt *runtime.NSMap[ShimInstance], events *exchange.Exchange, binaryCall *binary) {
	ctx = namespaces.WithNamespace(ctx, ns)
	ctx, cancel := timeout.WithContext(ctx, cleanupTimeout)
	defer cancel()

	log.G(ctx).WithFields(logrus.Fields{
		"id":        id,
		"namespace": ns,
	}).Warn("cleaning up after shim disconnected")
	response, err := binaryCall.Delete(ctx)
	if err != nil {
		log.G(ctx).WithError(err).WithFields(logrus.Fields{
			"id":        id,
			"namespace": ns,
		}).Warn("failed to clean up after shim disconnected")
	}

	if _, err := rt.Get(ctx, id); err != nil {
		// Task was never started or was already successfully deleted
		// No need to publish events
		return
	}

	var (
		pid        uint32
		exitStatus uint32
		exitedAt   time.Time
	)
	if response != nil {
		pid = response.Pid
		exitStatus = response.Status
		exitedAt = response.Timestamp
	} else {
		exitStatus = 255
		exitedAt = time.Now()
	}
	events.Publish(ctx, runtime.TaskExitEventTopic, &eventstypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         pid,
		ExitStatus:  exitStatus,
		ExitedAt:    protobuf.ToTimestamp(exitedAt),
	})

	events.Publish(ctx, runtime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: id,
		Pid:         pid,
		ExitStatus:  exitStatus,
		ExitedAt:    protobuf.ToTimestamp(exitedAt),
	})
}

// ShimInstance represents running shim process managed by ShimManager.
type ShimInstance interface {
	// ID of the shim.
	ID() string
	// Namespace of this shim.
	Namespace() string
	// Bundle is a file system path to shim's bundle.
	Bundle() string
	// Client returns the underlying TTRPC client for this shim.
	Client() *ttrpc.Client
	// Delete will close the client and remove bundle from disk.
	Delete(ctx context.Context) error
}

type shim struct {
	bundle *Bundle
	client *ttrpc.Client
}

var _ ShimInstance = (*shim)(nil)

// ID of the shim/task
func (s *shim) ID() string {
	return s.bundle.ID
}

func (s *shim) Namespace() string {
	return s.bundle.Namespace
}

func (s *shim) Bundle() string {
	return s.bundle.Path
}

func (s *shim) Client() *ttrpc.Client {
	return s.client
}

func (s *shim) Delete(ctx context.Context) error {
	var (
		result *multierror.Error
	)

	if err := s.client.Close(); err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to close ttrpc client: %w", err))
	}

	if err := s.client.UserOnCloseWait(ctx); err != nil {
		result = multierror.Append(result, fmt.Errorf("close wait error: %w", err))
	}

	if err := s.bundle.Delete(); err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to delete bundle")
		result = multierror.Append(result, fmt.Errorf("failed to delete bundle: %w", err))
	}

	return result.ErrorOrNil()
}

var _ runtime.Task = &shimTask{}

// shimTask wraps shim process and adds task service client for compatibility with existing shim manager.
type shimTask struct {
	ShimInstance
	task task.TaskService
}

func newShimTask(shim ShimInstance) *shimTask {
	return &shimTask{
		ShimInstance: shim,
		task:         task.NewTaskClient(shim.Client()),
	}
}

func (s *shimTask) Shutdown(ctx context.Context) error {
	_, err := s.task.Shutdown(ctx, &task.ShutdownRequest{
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

// PID of the task
func (s *shimTask) PID(ctx context.Context) (uint32, error) {
	response, err := s.task.Connect(ctx, &task.ConnectRequest{
		ID: s.ID(),
	})
	if err != nil {
		return 0, errdefs.FromGRPC(err)
	}

	return response.TaskPid, nil
}

func (s *shimTask) delete(ctx context.Context, sandboxed bool, removeTask func(ctx context.Context, id string)) (*runtime.Exit, error) {
	response, shimErr := s.task.Delete(ctx, &task.DeleteRequest{
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
	if shimErr == nil {
		removeTask(ctx, s.ID())
	}

	// Don't shut down sandbox as there may be other containers running.
	// Let controller decide when to shut down.
	if !sandboxed {
		if err := s.waitShutdown(ctx); err != nil {
			log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to shutdown shim task")
		}
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

func (s *shimTask) Create(ctx context.Context, opts runtime.CreateOpts) (runtime.Task, error) {
	topts := opts.TaskOptions
	if topts == nil || topts.GetValue() == nil {
		topts = opts.RuntimeOptions
	}
	request := &task.CreateTaskRequest{
		ID:         s.ID(),
		Bundle:     s.Bundle(),
		Stdin:      opts.IO.Stdin,
		Stdout:     opts.IO.Stdout,
		Stderr:     opts.IO.Stderr,
		Terminal:   opts.IO.Terminal,
		Checkpoint: opts.Checkpoint,
		Options:    protobuf.FromAny(topts),
	}
	for _, m := range opts.Rootfs {
		request.Rootfs = append(request.Rootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}

	_, err := s.task.Create(ctx, request)
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	return s, nil
}

func (s *shimTask) Pause(ctx context.Context) error {
	if _, err := s.task.Pause(ctx, &task.PauseRequest{
		ID: s.ID(),
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Resume(ctx context.Context) error {
	if _, err := s.task.Resume(ctx, &task.ResumeRequest{
		ID: s.ID(),
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Start(ctx context.Context) error {
	_, err := s.task.Start(ctx, &task.StartRequest{
		ID: s.ID(),
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Kill(ctx context.Context, signal uint32, all bool) error {
	if _, err := s.task.Kill(ctx, &task.KillRequest{
		ID:     s.ID(),
		Signal: signal,
		All:    all,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (runtime.ExecProcess, error) {
	if err := identifiers.Validate(id); err != nil {
		return nil, fmt.Errorf("invalid exec id %s: %w", id, err)
	}
	request := &task.ExecProcessRequest{
		ID:       s.ID(),
		ExecID:   id,
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
		Spec:     opts.Spec,
	}
	if _, err := s.task.Exec(ctx, request); err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return &process{
		id:   id,
		shim: s,
	}, nil
}

func (s *shimTask) Pids(ctx context.Context) ([]runtime.ProcessInfo, error) {
	resp, err := s.task.Pids(ctx, &task.PidsRequest{
		ID: s.ID(),
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	var processList []runtime.ProcessInfo
	for _, p := range resp.Processes {
		processList = append(processList, runtime.ProcessInfo{
			Pid:  p.Pid,
			Info: p.Info,
		})
	}
	return processList, nil
}

func (s *shimTask) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
	_, err := s.task.ResizePty(ctx, &task.ResizePtyRequest{
		ID:     s.ID(),
		Width:  size.Width,
		Height: size.Height,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) CloseIO(ctx context.Context) error {
	_, err := s.task.CloseIO(ctx, &task.CloseIORequest{
		ID:    s.ID(),
		Stdin: true,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Wait(ctx context.Context) (*runtime.Exit, error) {
	taskPid, err := s.PID(ctx)
	if err != nil {
		return nil, err
	}
	response, err := s.task.Wait(ctx, &task.WaitRequest{
		ID: s.ID(),
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return &runtime.Exit{
		Pid:       taskPid,
		Timestamp: protobuf.FromTimestamp(response.ExitedAt),
		Status:    response.ExitStatus,
	}, nil
}

func (s *shimTask) Checkpoint(ctx context.Context, path string, options *ptypes.Any) error {
	request := &task.CheckpointTaskRequest{
		ID:      s.ID(),
		Path:    path,
		Options: options,
	}
	if _, err := s.task.Checkpoint(ctx, request); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Update(ctx context.Context, resources *ptypes.Any, annotations map[string]string) error {
	if _, err := s.task.Update(ctx, &task.UpdateTaskRequest{
		ID:          s.ID(),
		Resources:   resources,
		Annotations: annotations,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) Stats(ctx context.Context) (*ptypes.Any, error) {
	response, err := s.task.Stats(ctx, &task.StatsRequest{
		ID: s.ID(),
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return response.Stats, nil
}

func (s *shimTask) Process(ctx context.Context, id string) (runtime.ExecProcess, error) {
	p := &process{
		id:   id,
		shim: s,
	}
	if _, err := p.State(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *shimTask) State(ctx context.Context) (runtime.State, error) {
	response, err := s.task.State(ctx, &task.StateRequest{
		ID: s.ID(),
	})
	if err != nil {
		if !errors.Is(err, ttrpc.ErrClosed) {
			return runtime.State{}, errdefs.FromGRPC(err)
		}
		return runtime.State{}, errdefs.ErrNotFound
	}
	return runtime.State{
		Pid:        response.Pid,
		Status:     statusFromProto(response.Status),
		Stdin:      response.Stdin,
		Stdout:     response.Stdout,
		Stderr:     response.Stderr,
		Terminal:   response.Terminal,
		ExitStatus: response.ExitStatus,
		ExitedAt:   protobuf.FromTimestamp(response.ExitedAt),
	}, nil
}

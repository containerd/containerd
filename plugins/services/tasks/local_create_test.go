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

package tasks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	api "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// cleanupCall is one cleanup-relevant operation and the context it ran with.
type cleanupCall struct {
	op       string
	id       string
	ns       string
	ctxErr   error
	deadline bool
}

type cleanupRecorder struct{ calls []cleanupCall }

func (r *cleanupRecorder) record(ctx context.Context, op, id string) {
	ns, _ := namespaces.Namespace(ctx)
	_, deadline := ctx.Deadline()
	r.calls = append(r.calls, cleanupCall{op: op, id: id, ns: ns, ctxErr: ctx.Err(), deadline: deadline})
}

type fakeTask struct {
	runtime.Task
	*cleanupRecorder
	id         string
	pidErr     error
	killErr    error
	waitBlocks bool
}

func (t *fakeTask) ID() string { return t.id }

func (t *fakeTask) PID(context.Context) (uint32, error) { return 0, t.pidErr }

func (t *fakeTask) Kill(ctx context.Context, _ uint32, _ bool) error {
	t.record(ctx, "kill", t.id)
	return t.killErr
}

func (t *fakeTask) Wait(ctx context.Context) (*runtime.Exit, error) {
	t.record(ctx, "wait", t.id)
	if t.waitBlocks {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &runtime.Exit{}, nil
}

// fakeRuntime hands out copies of task from Create.
type fakeRuntime struct {
	runtime.PlatformRuntime
	*cleanupRecorder
	task      fakeTask
	deleteErr error
	// deleteNeedsWait makes Delete reject a task whose exit was not waited for,
	// like a shim refusing to delete a running task.
	deleteNeedsWait bool
	deleted         bool
}

func (r *fakeRuntime) Get(context.Context, string) (runtime.Task, error) {
	return nil, errdefs.ErrNotFound
}

func (r *fakeRuntime) Create(_ context.Context, id string, _ runtime.CreateOpts) (runtime.Task, error) {
	t := r.task
	t.id = id
	t.cleanupRecorder = r.cleanupRecorder
	return &t, nil
}

func (r *fakeRuntime) Delete(ctx context.Context, id string) (*runtime.Exit, error) {
	r.record(ctx, "delete", id)
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	if r.deleteNeedsWait && !slices.ContainsFunc(r.calls, func(c cleanupCall) bool { return c.op == "wait" }) {
		return nil, errdefs.ErrFailedPrecondition
	}
	r.deleted = true
	return &runtime.Exit{}, nil
}

type fakeMonitor struct {
	*cleanupRecorder
	err error
}

func (m *fakeMonitor) Monitor(runtime.Task, map[string]string) error { return m.err }

// Stop takes no context, so the call carries no namespace or deadline.
func (m *fakeMonitor) Stop(t runtime.Task) error {
	m.record(context.Background(), "stop", t.ID())
	return nil
}

type fakeContainers struct{ containers.Store }

func (fakeContainers) Get(_ context.Context, id string) (containers.Container, error) {
	return containers.Container{ID: id, Runtime: containers.RuntimeInfo{Name: "io.containerd.runc.v2"}}, nil
}

// A task that Create fails to report to the caller must be killed and deleted:
// the caller cannot clean up a task it was never told about, and the leftover
// record makes deleting the container fail forever.
func TestCreateCleansUpTaskOnFailure(t *testing.T) {
	const id = "c1"
	errPID := errors.New("pid failed")
	errMonitor := errors.New("monitor failed")

	// Short budget so the blocked Wait row finishes quickly.
	old := createCleanupTimeout
	createCleanupTimeout = 50 * time.Millisecond
	t.Cleanup(func() { createCleanupTimeout = old })

	// ops lists the expected cleanup calls. Everything except the monitor stop
	// must run in the request's namespace on a live context with a deadline.
	ops := func(names ...string) []cleanupCall {
		var calls []cleanupCall
		for _, op := range names {
			c := cleanupCall{op: op, id: id}
			if op != "stop" {
				c.ns, c.deadline = "test-ns", true
			}
			calls = append(calls, c)
		}
		return calls
	}

	full := ops("stop", "kill", "wait", "delete")

	for _, tc := range []struct {
		name       string
		pidErr     error
		monitorErr error
		killErr    error
		waitBlocks bool
		deleteErr  error
		// deleteNeedsWait rows also assert that the task really was deleted.
		deleteNeedsWait bool
		wantCalls       []cleanupCall
	}{
		{name: "pid_error", pidErr: errPID, wantCalls: full},
		{name: "monitor_error", monitorErr: errMonitor, wantCalls: full},
		{name: "kill_fails", pidErr: errPID, killErr: errors.New("kill failed"), wantCalls: ops("stop", "kill", "delete")},
		{name: "kill_not_found", pidErr: errPID, killErr: fmt.Errorf("already exited: %w", errdefs.ErrNotFound), deleteNeedsWait: true, wantCalls: full},
		{name: "wait_blocks", pidErr: errPID, waitBlocks: true, wantCalls: full},
		{name: "delete_fails", pidErr: errPID, deleteErr: errors.New("delete failed"), wantCalls: full},
		{name: "no_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(namespaces.WithNamespace(t.Context(), "test-ns"))
			defer cancel()
			if tc.wantCalls != nil {
				// Cleanup must not depend on the request context, which is
				// what a cancelled client call leaves behind.
				cancel()
			}

			rec := &cleanupRecorder{}
			rt := &fakeRuntime{
				cleanupRecorder: rec,
				task:            fakeTask{pidErr: tc.pidErr, killErr: tc.killErr, waitBlocks: tc.waitBlocks},
				deleteErr:       tc.deleteErr,
				deleteNeedsWait: tc.deleteNeedsWait,
			}
			l := &local{
				containers: fakeContainers{},
				monitor:    &fakeMonitor{cleanupRecorder: rec, err: tc.monitorErr},
				v2Runtime:  rt,
			}

			_, err := l.Create(ctx, &api.CreateTaskRequest{ContainerID: id})

			if want := cmp.Or(tc.monitorErr, tc.pidErr); want != nil {
				require.ErrorIs(t, err, want)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantCalls, rec.calls)
			if tc.deleteNeedsWait {
				assert.True(t, rt.deleted, "task was not deleted")
			}
		})
	}
}

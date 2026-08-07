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
	"sync/atomic"
	"testing"
	"time"

	api "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	runtimeapi "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/pkg/timeout"
)

func TestShouldCleanupShim(t *testing.T) {
	otherErr := errors.New("some other error")

	testCases := []struct {
		Name     string
		SgetErr  error
		PidErr   error
		PInfo    []runtimeapi.ProcessInfo
		Expected bool
	}{
		{
			Name:     "sandbox found",
			SgetErr:  nil,
			PidErr:   nil,
			PInfo:    nil,
			Expected: false,
		},
		{
			Name:     "sandbox lookup fails with unrelated error",
			SgetErr:  otherErr,
			PidErr:   nil,
			PInfo:    nil,
			Expected: false,
		},
		{
			Name:     "not a sandbox, no pids running",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   nil,
			PInfo:    []runtimeapi.ProcessInfo{},
			Expected: true,
		},
		{
			Name:     "not a sandbox, pids still running",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   nil,
			PInfo:    []runtimeapi.ProcessInfo{{Pid: 1234}},
			Expected: false,
		},
		{
			Name:     "not a sandbox, pids lookup returns not found",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   errdefs.ErrNotFound,
			PInfo:    nil,
			Expected: true,
		},
		{
			Name:     "not a sandbox, pids lookup fails with other error",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   otherErr,
			PInfo:    nil,
			Expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			require.Equal(t, tc.Expected, shouldCleanupShim(tc.SgetErr, tc.PidErr, tc.PInfo))
		})
	}
}

// hangingTaskClient is a task client for a shim which answers every request
// except Delete, which is silently dropped and never replied to. This mirrors
// the reported production failure, where the leftover shims kept serving other
// requests and only the task teardown request went unanswered.
//
// Embedding the interface leaves every other method nil, so an unexpected call
// panics instead of silently passing.
type hangingTaskClient struct {
	TaskServiceClient

	deleteCalled   atomic.Bool
	shutdownCalled atomic.Bool
	// shutdownCtxLive records whether Shutdown was handed a context which had
	// not already expired.
	shutdownCtxLive atomic.Bool
}

func (c *hangingTaskClient) Delete(ctx context.Context, _ *api.DeleteRequest) (*api.DeleteResponse, error) {
	c.deleteCalled.Store(true)
	// A ttrpc call only gives up once the caller's context is done, so an
	// unbounded context blocks here forever.
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *hangingTaskClient) Shutdown(ctx context.Context, _ *api.ShutdownRequest) (*emptypb.Empty, error) {
	c.shutdownCtxLive.Store(ctx.Err() == nil)
	c.shutdownCalled.Store(true)
	return &emptypb.Empty{}, nil
}

type fakeShimInstance struct {
	ShimInstance

	id          string
	closeCalled atomic.Bool
}

func (f *fakeShimInstance) ID() string                   { return f.id }
func (f *fakeShimInstance) Endpoint() (string, int)      { return "ttrpc+unix://fake.sock", 3 }
func (f *fakeShimInstance) Delete(context.Context) error { return nil }

func (f *fakeShimInstance) Close() error {
	f.closeCalled.Store(true)
	return nil
}

// TestCleanupLeakedShimDoesNotHang covers the startup hang reported in #13848.
// loadShims loads bundles through an errgroup limited to GOMAXPROCS, so a
// cleanup which never returns holds its slot forever and eg.Wait never
// completes: containerd never creates its socket, systemd kills it on the
// start timeout, and the next start hits the very same shim again.
func TestCleanupLeakedShimDoesNotHang(t *testing.T) {
	const shortTimeout = 100 * time.Millisecond

	orig := timeout.Get(cleanupTimeout)
	timeout.Set(cleanupTimeout, shortTimeout)
	t.Cleanup(func() { timeout.Set(cleanupTimeout, orig) })

	client := &hangingTaskClient{}
	instance := &fakeShimInstance{id: "leaked-shim"}
	shim := &shimTask{ShimInstance: instance, task: client}

	done := make(chan error, 1)
	go func() {
		done <- cleanupLeakedShim(context.Background(), shim)
	}()

	select {
	case err := <-done:
		// Note that the error is not comparable to context.DeadlineExceeded:
		// errgrpc.ToNative rewrites it into an opaque error of its own.
		require.Error(t, err, "cleaning up an unresponsive shim should report failure")
	case <-time.After(10 * shortTimeout):
		t.Fatal("cleanupLeakedShim never returned: loadShims would hang forever at startup")
	}

	require.True(t, client.deleteCalled.Load(), "Task.Delete should have been attempted")
	require.True(t, client.shutdownCalled.Load(), "shim should be shut down after Task.Delete timed out")
	require.True(t, client.shutdownCtxLive.Load(), "forced shutdown needs a context which has not already expired")
	require.True(t, instance.closeCalled.Load(), "shim connection should be closed after Task.Delete timed out")
}

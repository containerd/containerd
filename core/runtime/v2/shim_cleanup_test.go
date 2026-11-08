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
	"net"
	"sync/atomic"
	"testing"
	"time"

	task "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// failDeleteTask is a task client whose Delete fails.
type failDeleteTask struct {
	TaskServiceClient
}

func (failDeleteTask) Delete(context.Context, *task.DeleteRequest) (*task.DeleteResponse, error) {
	return nil, errors.New("delete failed")
}

func (failDeleteTask) Shutdown(context.Context, *task.ShutdownRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// TestCleanupShimTaskWaitsForOnClose verifies that a failed cleanup returns
// only after the on-close callback is done, so a delayed one cannot act on a
// task that reuses the ID once the bundle is deleted.
func TestCleanupShimTaskWaitsForOnClose(t *testing.T) {
	ctx, root, state := bundleDirs(t)
	b, err := NewBundle(ctx, root, state, "delayed-close", nil)
	require.NoError(t, err)

	cconn, sconn := net.Pipe()
	defer sconn.Close()
	var (
		release = make(chan struct{})
		closed  atomic.Bool
	)
	client := ttrpc.NewClient(cconn, ttrpc.WithOnClose(func() {
		<-release
		closed.Store(true)
	}))
	st := &shimTask{ShimInstance: &shim{bundle: b, client: client}, task: failDeleteTask{}}

	errCh := make(chan error, 1)
	go func() { errCh <- cleanupShimTask(ctx, st) }()

	select {
	case err := <-errCh:
		t.Fatalf("cleanup returned before the on-close callback finished: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)

	assert.ErrorContains(t, <-errCh, "delete failed")
	assert.True(t, closed.Load(), "on-close callback must be done")
}

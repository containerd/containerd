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
	"testing"

	api "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestShimTaskDeleteUsesCleanupContextAfterTaskDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	taskClient := &fakeTaskServiceClient{
		afterDelete: cancel,
	}
	shim := &fakeShimInstance{id: "test-task"}
	shimTask := &shimTask{
		ShimInstance: shim,
		task:         taskClient,
	}

	var removeTaskErrs []error
	_, err := shimTask.delete(ctx, false, func(ctx context.Context, id string) {
		require.Equal(t, shim.ID(), id)
		removeTaskErrs = append(removeTaskErrs, ctx.Err())
	})
	require.NoError(t, err)
	require.NoError(t, taskClient.deleteCtxErr)
	require.NoError(t, taskClient.shutdownCtxErr)
	require.NoError(t, shim.deleteCtxErr)
	require.Len(t, removeTaskErrs, 2)
	require.ErrorIs(t, removeTaskErrs[0], context.Canceled)
	require.NoError(t, removeTaskErrs[1])
}

type fakeShimInstance struct {
	id           string
	deleteCtxErr error
}

func (f *fakeShimInstance) Close() error {
	return nil
}

func (f *fakeShimInstance) ID() string {
	return f.id
}

func (f *fakeShimInstance) Namespace() string {
	return "default"
}

func (f *fakeShimInstance) Bundle() string {
	return ""
}

func (f *fakeShimInstance) Client() any {
	return nil
}

func (f *fakeShimInstance) Delete(ctx context.Context) error {
	f.deleteCtxErr = ctx.Err()
	return f.deleteCtxErr
}

func (f *fakeShimInstance) Endpoint() (string, int) {
	return "", CurrentShimVersion
}

type fakeTaskServiceClient struct {
	TaskServiceClient

	afterDelete    func()
	deleteCtxErr   error
	shutdownCtxErr error
}

func (f *fakeTaskServiceClient) Delete(ctx context.Context, _ *api.DeleteRequest) (*api.DeleteResponse, error) {
	f.deleteCtxErr = ctx.Err()
	if f.afterDelete != nil {
		f.afterDelete()
	}
	return &api.DeleteResponse{}, nil
}

func (f *fakeTaskServiceClient) Shutdown(ctx context.Context, _ *api.ShutdownRequest) (*emptypb.Empty, error) {
	f.shutdownCtxErr = ctx.Err()
	return &emptypb.Empty{}, f.shutdownCtxErr
}

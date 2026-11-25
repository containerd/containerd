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
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/events/exchange"
	runtimeapi "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

type notFoundContainerStore struct{ containers.Store }

func (notFoundContainerStore) Get(context.Context, string) (containers.Container, error) {
	return containers.Container{}, errdefs.ErrNotFound
}

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
		{
			// Not answering in time is not proof of a dead shim.
			Name:     "not a sandbox, pids lookup times out",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   context.DeadlineExceeded,
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

func TestLoadShimKeepsBundleWhenBinaryMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses the unix shim log")
	}
	ctx := namespaces.WithNamespace(context.Background(), "test")
	bundle := &Bundle{ID: "id", Path: t.TempDir(), Namespace: "test"}
	gone := filepath.Join(t.TempDir(), "gone", "containerd-shim-runc-v2")
	require.NoError(t, os.WriteFile(filepath.Join(bundle.Path, "shim-binary-path"), []byte(gone), 0o600))

	m := &ShimManager{
		containers: notFoundContainerStore{},
		shims:      runtimeapi.NewNSMap[ShimInstance](),
		events:     exchange.NewExchange(),
	}
	// It gets as far as connecting, which fails for lack of a shim.
	require.ErrorContains(t, m.loadShim(ctx, bundle), "unable to load shim")
	require.DirExists(t, bundle.Path)
}

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
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/containers"
	runtimeapi "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/core/sandbox"
	shimbinary "github.com/containerd/containerd/v2/pkg/shim"
)

// fakeContainerStore stubs containers.Store; only Get is used by the code under
// test. The embedded nil interface panics if any other method is called.
type fakeContainerStore struct {
	containers.Store
	container containers.Container
	err       error
}

func (f fakeContainerStore) Get(context.Context, string) (containers.Container, error) {
	return f.container, f.err
}

// fakeSandboxStore stubs sandbox.Store; only Get is used by the code under test.
type fakeSandboxStore struct {
	sandbox.Store
	sandbox sandbox.Sandbox
	err     error
}

func (f fakeSandboxStore) Get(context.Context, string) (sandbox.Sandbox, error) {
	return f.sandbox, f.err
}

func container(name string) containers.Container {
	return containers.Container{Runtime: containers.RuntimeInfo{Name: name}}
}

func sbox(name string) sandbox.Sandbox {
	return sandbox.Sandbox{Runtime: sandbox.RuntimeOpts{Name: name}}
}

func TestRuntimeName(t *testing.T) {
	otherErr := errors.New("some other error")

	testCases := []struct {
		Name       string
		Containers containers.Store
		Sandboxes  sandbox.Store
		Expected   string
	}{
		{
			Name:       "container record wins",
			Containers: fakeContainerStore{container: container("io.containerd.runc.v2")},
			Sandboxes:  fakeSandboxStore{sandbox: sbox("io.containerd.other.v1")},
			Expected:   "io.containerd.runc.v2",
		},
		{
			Name:       "falls back to sandbox when no container",
			Containers: fakeContainerStore{err: errdefs.ErrNotFound},
			Sandboxes:  fakeSandboxStore{sandbox: sbox("io.containerd.runc.v2")},
			Expected:   "io.containerd.runc.v2",
		},
		{
			Name:       "empty when neither store has a record",
			Containers: fakeContainerStore{err: errdefs.ErrNotFound},
			Sandboxes:  fakeSandboxStore{err: errdefs.ErrNotFound},
			Expected:   "",
		},
		{
			// A non-NotFound container error is logged but still falls through
			// to the sandbox store.
			Name:       "container error still tries sandbox",
			Containers: fakeContainerStore{err: otherErr},
			Sandboxes:  fakeSandboxStore{sandbox: sbox("io.containerd.runc.v2")},
			Expected:   "io.containerd.runc.v2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			m := &ShimManager{containers: tc.Containers, sandboxStore: tc.Sandboxes}
			require.Equal(t, tc.Expected, m.runtimeName(context.Background(), "id"))
		})
	}
}

func TestResolveRuntimeWithFallback(t *testing.T) {
	const (
		stalePath   = "/nonexistent/stale/containerd-shim-runc-v2"
		runtimeName = "io.containerd.runc.v2"
		validPath   = "/usr/local/bin/containerd-shim-runc-v2"
	)

	testCases := []struct {
		Name        string
		Runtime     string
		RealFile    bool // create Runtime as a real file so os.Stat succeeds
		Containers  containers.Store
		Sandboxes   sandbox.Store
		Cache       map[string]string
		Expected    string
		ExpectError bool
	}{
		{
			// Pinned path still resolves: no metadata lookup, returned as-is.
			Name:     "pinned path still valid",
			RealFile: true,
		},
		{
			// Pinned path is stale but the runtime name in metadata re-resolves.
			Name:       "stale path re-resolved from runtime name",
			Runtime:    stalePath,
			Containers: fakeContainerStore{container: container(runtimeName)},
			Sandboxes:  fakeSandboxStore{err: errdefs.ErrNotFound},
			Cache:      map[string]string{shimbinary.BinaryName(runtimeName): validPath},
			Expected:   validPath,
		},
		{
			// Stale path with a sandbox record instead of a container.
			Name:       "stale path re-resolved from sandbox runtime name",
			Runtime:    stalePath,
			Containers: fakeContainerStore{err: errdefs.ErrNotFound},
			Sandboxes:  fakeSandboxStore{sandbox: sbox(runtimeName)},
			Cache:      map[string]string{shimbinary.BinaryName(runtimeName): validPath},
			Expected:   validPath,
		},
		{
			// Stale path and no metadata: original resolution error propagates.
			Name:        "stale path with no metadata errors",
			Runtime:     stalePath,
			Containers:  fakeContainerStore{err: errdefs.ErrNotFound},
			Sandboxes:   fakeSandboxStore{err: errdefs.ErrNotFound},
			ExpectError: true,
		},
		{
			// Metadata name equals the (stale) runtime: the guard skips a
			// pointless retry and the original error propagates.
			Name:        "stale path equal to metadata name errors",
			Runtime:     stalePath,
			Containers:  fakeContainerStore{container: container(stalePath)},
			Sandboxes:   fakeSandboxStore{err: errdefs.ErrNotFound},
			ExpectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			m := &ShimManager{containers: tc.Containers, sandboxStore: tc.Sandboxes}
			for name, path := range tc.Cache {
				m.runtimePaths.Store(name, path)
			}
			runtime, expected := tc.Runtime, tc.Expected
			if tc.RealFile {
				runtime = filepath.Join(t.TempDir(), "containerd-shim-runc-v2")
				require.NoError(t, os.WriteFile(runtime, nil, 0o755))
				expected = runtime
			}
			got, err := m.resolveRuntimeWithFallback(context.Background(), runtime, "id")
			if tc.ExpectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, expected, got)
		})
	}
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

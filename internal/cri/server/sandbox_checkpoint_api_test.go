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

package server

import (
	"context"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkpointControllerStub struct {
	sandbox.Controller
	checkpoint func(context.Context, string, sandbox.CheckpointOptions) error
	restore    func(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error)
}

func (c *checkpointControllerStub) Checkpoint(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) error {
	return c.checkpoint(ctx, sandboxID, opts)
}

func (c *checkpointControllerStub) Restore(ctx context.Context, sandboxID string, opts sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return c.restore(ctx, sandboxID, opts)
}

type checkpointSandboxService struct {
	sandboxService
	controller sandbox.Controller
	sandboxer  string
	err        error
}

func (s *checkpointSandboxService) SandboxController(sandboxer string) (sandbox.Controller, error) {
	s.sandboxer = sandboxer
	return s.controller, s.err
}

func checkpointAPIContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestPodCheckpointRequestsRequireDeadline(t *testing.T) {
	err := requirePodCheckpointDeadline(context.Background(), "pod checkpoint")
	require.ErrorContains(t, err, "finite context deadline")
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, deadlineCancel := context.WithTimeout(ctx, time.Second)
	defer deadlineCancel()
	require.ErrorIs(t, requirePodCheckpointDeadline(ctx, "pod checkpoint"), context.Canceled)
}

func TestCheckpointControllerCapability(t *testing.T) {
	cri := newTestCRIService()
	service := &checkpointSandboxService{controller: &fakeSandboxController{}}
	cri.sandboxService = service

	_, err := cri.checkpointController("vm-runtime", "restore")
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)
	assert.Equal(t, "vm-runtime", service.sandboxer)
}

func TestReserveContainerCheckpoints(t *testing.T) {
	cri := newTestCRIService()
	release, err := cri.reserveContainerCheckpoints([]string{"container-a", "container-b"})
	require.NoError(t, err)

	_, err = cri.reserveContainerCheckpoints([]string{"container-b"})
	require.ErrorContains(t, err, "already in progress")

	release()
	releaseAgain, err := cri.reserveContainerCheckpoints([]string{"container-b"})
	require.NoError(t, err)
	releaseAgain()
}

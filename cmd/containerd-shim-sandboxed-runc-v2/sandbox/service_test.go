//go:build linux

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

package sandbox

import (
	"context"
	"testing"
	"time"

	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/pkg/shutdown"
)

func TestServiceWithoutSandbox(t *testing.T) {
	ctx := context.Background()
	_, sd := shutdown.WithShutdown(ctx)
	s := NewService(sd)

	// Every sandbox scoped call is NotFound until a sandbox is created.
	_, err := s.StartSandbox(ctx, &api.StartSandboxRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "start: %v", err)
	_, err = s.StopSandbox(ctx, &api.StopSandboxRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "stop: %v", err)
	_, err = s.SandboxStatus(ctx, &api.SandboxStatusRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "status: %v", err)
	_, err = s.PingSandbox(ctx, &api.PingRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "ping: %v", err)
	_, err = s.WaitSandbox(ctx, &api.WaitSandboxRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "wait: %v", err)
	_, err = s.SandboxMetrics(ctx, &api.SandboxMetricsRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotFound(errgrpc.ToNative(err)), "metrics: %v", err)

	_, err = s.UpdateSandbox(ctx, &api.UpdateSandboxRequest{SandboxID: testID})
	assert.True(t, errdefs.IsNotImplemented(errgrpc.ToNative(err)), "update: %v", err)

	resp, err := s.Platform(ctx, &api.PlatformRequest{SandboxID: testID})
	require.NoError(t, err)
	assert.Equal(t, "linux", resp.GetPlatform().GetOS())

	// Shutting down, even an unknown sandbox, ends the shim.
	select {
	case <-sd.Done():
		t.Fatal("the shim must not shut down before ShutdownSandbox")
	default:
	}
	_, err = s.ShutdownSandbox(ctx, &api.ShutdownSandboxRequest{SandboxID: testID})
	require.NoError(t, err)
	select {
	case <-sd.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ShutdownSandbox must end the shim")
	}
}

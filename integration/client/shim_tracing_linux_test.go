//go:build shim_tracing

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

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// TestShimTraceContextPropagation checks that the trace context of an API call
// reaches the OCI hooks runc executes on behalf of the shim.
//
// Only a shim built with the shim_tracing tag propagates it, hence the build
// tag on this file.
func TestShimTraceContextPropagation(t *testing.T) {
	const (
		traceID    = "0af7651916cd43dd8448eb211c80319c"
		tracestate = "vendor=value"
		bag        = "pod.name=nginx"
	)

	// Both the daemon and the shim install the W3C propagators only when an
	// exporter endpoint is configured, and they read it at startup, hence the
	// dedicated daemon. Nothing has to listen on the endpoint, the spans
	// themselves are of no interest here.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")

	_, ctrd, cleanup := newDaemonWithConfig(t, "version = 3\n")
	defer cleanup()

	client, err := newClient(t, ctrd.addr)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	// Send a known trace context with every call for the daemon to extract.
	ctx = metadata.AppendToOutgoingContext(ctx,
		"traceparent", "00-"+traceID+"-b7ad6b7169203331-01",
		"tracestate", tracestate,
		"baggage", bag,
	)

	image, err := client.Pull(ctx, testImage, WithPullUnpack)
	require.NoError(t, err)

	envFile := filepath.Join(t.TempDir(), "hook-env")
	withEnvDumpHook := func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		if s.Hooks == nil {
			s.Hooks = &specs.Hooks{}
		}
		s.Hooks.CreateRuntime = []specs.Hook{{
			Path: "/bin/sh",
			Args: []string{"sh", "-c", `env > "$1"`, "sh", envFile},
			// Env is left unset on purpose: a hook declaring its own
			// environment does not inherit the trace context.
		}}
		return nil
	}

	id := t.Name()
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), shortCommand, withEnvDumpHook),
	)
	require.NoError(t, err)
	defer container.Delete(ctx, WithSnapshotCleanup)

	// Creating the task runs runc create, and with it the CreateRuntime hooks.
	task, err := container.NewTask(ctx, empty())
	require.NoError(t, err)
	defer task.Delete(ctx, WithProcessKill)

	env, err := os.ReadFile(envFile)
	require.NoError(t, err)
	assert.Contains(t, string(env), "TRACEPARENT=00-"+traceID+"-")
	assert.Contains(t, string(env), "TRACESTATE="+tracestate+"\n")
	assert.Contains(t, string(env), "BAGGAGE="+bag+"\n")
}

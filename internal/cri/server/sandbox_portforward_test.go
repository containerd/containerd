//go:build linux || windows

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
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// nopReadWriteCloser is a client stream that never produces data.
type nopReadWriteCloser struct{}

func (nopReadWriteCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (nopReadWriteCloser) Write(b []byte) (int, error) { return len(b), nil }
func (nopReadWriteCloser) Close() error                { return nil }

// portForwardTestSandbox returns a host-network sandbox, so the host path dials loopback
// directly.
func portForwardTestSandbox(id, endpoint string) sandboxstore.Sandbox {
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             id,
			Name:           id,
			RuntimeHandler: "runc",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: id},
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE,
						},
					},
				},
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{
						HostProcess: true,
					},
				},
			},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	sb.Sandboxer = "shim"
	sb.Endpoint = sandboxstore.Endpoint{Version: 2, Address: endpoint}
	return sb
}

// listenLoopback starts a loopback listener that drains its input, and returns its port.
func listenLoopback(t *testing.T) int32 {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(io.Discard, conn)
			}()
		}
	}()
	return int32(l.Addr().(*net.TCPAddr).Port)
}

// TestPortForwardDispatch covers which path portForward selects, and that it falls back to
// the host only on ErrNotImplemented.
func TestPortForwardDispatch(t *testing.T) {
	const endpoint = "ttrpc+unix:///run/test.sock"

	for _, tc := range []struct {
		name            string
		portForwardType string
		endpoint        string
		// runtimeHandler overrides the sandbox's handler; empty means the configured one.
		runtimeHandler string
		controllerErr  error
		// wantCalls is the number of PortForwardSandbox calls expected.
		wantCalls int
		// wantErr is a substring of the expected error; empty means the host path ran
		// to completion.
		wantErr string
	}{
		{
			name:            "default config never calls the sandbox",
			portForwardType: "",
			endpoint:        endpoint,
			wantCalls:       0,
		},
		{
			name:            "explicit host never calls the sandbox",
			portForwardType: criconfig.PortForwardTypeHost,
			endpoint:        endpoint,
			wantCalls:       0,
		},
		{
			name:            "sandbox type without an endpoint falls back to the host",
			portForwardType: criconfig.PortForwardTypeSandbox,
			endpoint:        "",
			wantCalls:       0,
		},
		{
			name:            "unconfigured runtime handler falls back to the host",
			portForwardType: criconfig.PortForwardTypeSandbox,
			endpoint:        endpoint,
			runtimeHandler:  "gone",
			wantCalls:       0,
		},
		{
			name:            "ErrNotImplemented falls back to the host",
			portForwardType: criconfig.PortForwardTypeSandbox,
			endpoint:        endpoint,
			controllerErr:   errdefs.ErrNotImplemented,
			wantCalls:       1,
		},
		{
			name:            "other errors are returned, not fallen back",
			portForwardType: criconfig.PortForwardTypeSandbox,
			endpoint:        endpoint,
			controllerErr:   errors.New("controller exploded"),
			wantCalls:       1,
			wantErr:         "controller exploded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := listenLoopback(t)

			c := newTestCRIService()
			runtimeCfg := c.config.ContainerdConfig.Runtimes["runc"]
			runtimeCfg.PortForwardType = tc.portForwardType
			c.config.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{"runc": runtimeCfg}

			fake := &fakeSandboxService{portForwardErr: tc.controllerErr}
			c.sandboxService = fake

			sb := portForwardTestSandbox("sb-1", tc.endpoint)
			if tc.runtimeHandler != "" {
				sb.RuntimeHandler = tc.runtimeHandler
			}
			require.NoError(t, c.sandboxStore.Add(sb))

			err := c.portForward(context.Background(), "sb-1", port, nopReadWriteCloser{})

			assert.Len(t, fake.portForwardCalls, tc.wantCalls)
			if tc.wantCalls > 0 {
				call := fake.portForwardCalls[0]
				assert.Equal(t, "shim", call.sandboxer)
				assert.Equal(t, "sb-1", call.sandboxID)
				assert.Equal(t, port, call.port)
				assert.True(t, strings.HasPrefix(call.streamID, "portforward-"),
					"stream ID %q should be prefixed", call.streamID)
			}

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				// The host path reached the listener and copied to completion.
				require.NoError(t, err)
			}
		})
	}
}

// TestPortForwardUnknownSandbox checks the store lookup happens before any dispatch.
func TestPortForwardUnknownSandbox(t *testing.T) {
	c := newTestCRIService()
	err := c.portForward(context.Background(), "missing", 8080, nopReadWriteCloser{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find sandbox")
}

// TestCopyPortForward checks bytes move in both directions and that copying ends when
// one side closes.
func TestCopyPortForward(t *testing.T) {
	t.Run("copies both directions", func(t *testing.T) {
		clientSide, streamSide := net.Pipe()
		upstreamSide, connSide := net.Pipe()
		defer clientSide.Close()
		defer upstreamSide.Close()

		done := make(chan error, 1)
		go func() {
			done <- copyPortForward(context.Background(), "sb-1", 8080, connSide, streamSide)
		}()

		// client -> upstream
		_, err := clientSide.Write([]byte("ping"))
		require.NoError(t, err)
		buf := make([]byte, 4)
		_, err = io.ReadFull(upstreamSide, buf)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(buf))

		// upstream -> client
		_, err = upstreamSide.Write([]byte("pong"))
		require.NoError(t, err)
		_, err = io.ReadFull(clientSide, buf)
		require.NoError(t, err)
		assert.Equal(t, "pong", string(buf))

		// Closing the client ends the copy, within the grace period.
		clientSide.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("copyPortForward did not return after the client closed")
		}
	})

	t.Run("returns when the context is cancelled", func(t *testing.T) {
		_, streamSide := net.Pipe()
		_, connSide := net.Pipe()
		defer streamSide.Close()
		defer connSide.Close()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- copyPortForward(ctx, "sb-1", 8080, connSide, streamSide)
		}()

		cancel()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("copyPortForward did not return after cancellation")
		}
	})
}

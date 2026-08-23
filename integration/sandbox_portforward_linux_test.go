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

package integration

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/integration/images"
)

// portForwardRuntimeHandler is configured with `port_forward_type = "sandbox"` by
// script/test/utils.sh.
const portForwardRuntimeHandler = "runc-pf"

// forwardPort mints a port forward URL for sb, opens it, and returns the local port that
// is now forwarded to targetPort inside the sandbox.
func forwardPort(t *testing.T, sb string, targetPort int32) int {
	t.Helper()

	rsp, err := runtimeService.PortForward(&criruntime.PortForwardRequest{
		PodSandboxId: sb,
		Port:         []int32{targetPort},
	})
	require.NoError(t, err)
	require.NotEmpty(t, rsp.Url)

	u, err := url.Parse(rsp.Url)
	require.NoError(t, err)

	transport, upgrader, err := spdy.RoundTripperFor(&rest.Config{})
	require.NoError(t, err)
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", u)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	var out, errOut bytes.Buffer
	// Port 0 makes the forwarder pick a free local port. Bind one address only, since
	// each address would otherwise get a different port and only the last is reported.
	pf, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"},
		[]string{fmt.Sprintf("0:%d", targetPort)}, stopCh, readyCh, &out, &errOut)
	require.NoError(t, err)

	doneCh := make(chan error, 1)
	go func() { doneCh <- pf.ForwardPorts() }()
	t.Cleanup(func() {
		close(stopCh)
		<-doneCh
	})

	select {
	case <-readyCh:
	case err := <-doneCh:
		t.Fatalf("port forward exited before becoming ready: %v (out=%q err=%q)", err, out.String(), errOut.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for port forward to become ready (out=%q err=%q)", out.String(), errOut.String())
	}

	ports, err := pf.GetPorts()
	require.NoError(t, err)
	require.Len(t, ports, 1)
	return int(ports[0].Local)
}

// TestSandboxPortForward starts an HTTP server inside a sandbox and reads from it through
// a CRI port forward, exercising the forwarded bytes rather than just the minted URL.
func TestSandboxPortForward(t *testing.T) {
	testSandboxPortForward(t, *runtimeHandler)
}

// TestSandboxPortForwardFallback runs the same check against `port_forward_type =
// "sandbox"` on a podsandbox runtime, which must fall back to the host network namespace.
func TestSandboxPortForwardFallback(t *testing.T) {
	testSandboxPortForward(t, portForwardRuntimeHandler)
}

func testSandboxPortForward(t *testing.T, handler string) {
	const targetPort = 8080

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "port-forward")
	sb, err := runtimeService.RunPodSandbox(sbConfig, handler)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	})

	t.Log("Create a container serving over HTTP")
	cnConfig := ContainerConfig(
		"server",
		testImage,
		WithCommand("sh", "-c",
			fmt.Sprintf("while true; do printf 'HTTP/1.1 200 OK\\r\\nContent-Length: 5\\r\\n\\r\\nhello' | nc -l -p %d; done", targetPort)),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for the server to listen")
	require.NoError(t, Eventually(func() (bool, error) {
		status, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		return status.GetState() == criruntime.ContainerState_CONTAINER_RUNNING, nil
	}, time.Second, 30*time.Second))

	t.Log("Forward the port and read from the server")
	local := forwardPort(t, sb, targetPort)

	// The server accepts one connection at a time, so retry while it loops around.
	var body string
	require.NoError(t, Eventually(func() (bool, error) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)), 5*time.Second)
		if err != nil {
			return false, nil
		}
		defer conn.Close()

		if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")); err != nil {
			return false, nil
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(conn); err != nil {
			return false, nil
		}
		body = buf.String()
		return body != "", nil
	}, time.Second, 60*time.Second))

	assert.Contains(t, body, "hello")
}

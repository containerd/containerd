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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	spdydialer "k8s.io/client-go/transport/spdy"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/cri-streaming/pkg/streaming/portforward"
	httpstreamspdy "k8s.io/streaming/pkg/httpstream/spdy"

	"github.com/containerd/containerd/v2/integration/images"
)

func TestContainerPortForward(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-portforward")
	sbStatus, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	require.NotNil(t, sbStatus.GetNetwork())
	podIP := sbStatus.GetNetwork().GetIp()
	require.NotEmpty(t, podIP)

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container-portforward"
		targetPort    = int32(8080)
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container listening on a port")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", fmt.Sprintf("nc -l -s %s -p %d -e cat", podIP, targetPort)),
	)

	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	testMsg := "hello portforward\n"
	var lastErr error
	err = Eventually(func() (bool, error) {
		pfResp, err := runtimeService.PortForward(&runtime.PortForwardRequest{
			PodSandboxId: sb,
			Port:         []int32{targetPort},
		})
		if err != nil {
			lastErr = err
			return false, nil
		}
		reqURL, err := url.Parse(pfResp.Url)
		if err != nil {
			lastErr = err
			return false, nil
		}
		transport, err := httpstreamspdy.NewRoundTripper(nil)
		if err != nil {
			lastErr = err
			return false, nil
		}
		upgrader := spdydialer.NewUpgraderForStreaming(transport)
		dialer := spdydialer.NewDialer(upgrader, &http.Client{Transport: transport, Timeout: 30 * time.Second}, "POST", reqURL)
		conn, _, err := dialer.Dial(portforward.PortForwardV1Name)
		if err != nil {
			lastErr = err
			return false, nil
		}
		defer conn.Close()

		headers := make(http.Header)
		headers.Set(portforward.StreamType, portforward.StreamTypeData)
		headers.Set(portforward.PortHeader, fmt.Sprintf("%d", targetPort))
		headers.Set(portforward.PortForwardRequestIDHeader, "1")
		dataStream, err := conn.CreateStream(headers)
		if err != nil {
			lastErr = err
			return false, nil
		}
		defer dataStream.Close()

		errHeaders := make(http.Header)
		errHeaders.Set(portforward.StreamType, portforward.StreamTypeError)
		errHeaders.Set(portforward.PortHeader, fmt.Sprintf("%d", targetPort))
		errHeaders.Set(portforward.PortForwardRequestIDHeader, "1")
		errorStream, err := conn.CreateStream(errHeaders)
		if err != nil {
			lastErr = err
			return false, nil
		}
		defer errorStream.Close()

		if _, err = dataStream.Write([]byte(testMsg)); err != nil {
			lastErr = err
			return false, nil
		}
		buf := make([]byte, len(testMsg))
		if _, err = io.ReadFull(dataStream, buf); err != nil {
			lastErr = err
			return false, nil
		}
		if string(buf) != testMsg {
			lastErr = fmt.Errorf("unexpected port-forward response %q", string(buf))
			return false, nil
		}
		return true, nil
	}, 100*time.Millisecond, 30*time.Second)
	if err != nil {
		if lastErr != nil {
			t.Fatalf("port-forward did not become ready: %v", lastErr)
		}
		require.NoError(t, err)
	}
}

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
	httpstreamspdy "k8s.io/apimachinery/pkg/util/httpstream/spdy"
	spdydialer "k8s.io/client-go/transport/spdy"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/cri-streaming/pkg/streaming/portforward"

	"github.com/containerd/containerd/v2/integration/images"
)

func TestContainerPortForward(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-portforward")

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
		WithCommand("sh", "-c", fmt.Sprintf("nc -l -p %d -e cat", targetPort)),
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

	t.Log("Request port forward URL from CRI")
	pfResp, err := runtimeService.PortForward(&runtime.PortForwardRequest{
		PodSandboxId: sb,
		Port:         []int32{targetPort},
	})
	require.NoError(t, err)
	require.NotEmpty(t, pfResp.Url)

	t.Logf("Connecting to port forward streaming endpoint: %s", pfResp.Url)
	reqURL, err := url.Parse(pfResp.Url)
	require.NoError(t, err)

	transport, err := httpstreamspdy.NewRoundTripper(nil)
	require.NoError(t, err)

	dialer := spdydialer.NewDialer(transport, &http.Client{Timeout: 30 * time.Second}, "POST", reqURL)
	conn, _, err := dialer.Dial(portforward.PortForwardV1Name)
	require.NoError(t, err)
	defer conn.Close()

	headers := make(http.Header)
	headers.Set(portforward.StreamType, portforward.StreamTypeData)
	headers.Set(portforward.PortHeader, fmt.Sprintf("%d", targetPort))
	headers.Set(portforward.PortForwardRequestIDHeader, "1")

	dataStream, err := conn.CreateStream(headers)
	require.NoError(t, err)

	errHeaders := make(http.Header)
	errHeaders.Set(portforward.StreamType, portforward.StreamTypeError)
	errHeaders.Set(portforward.PortHeader, fmt.Sprintf("%d", targetPort))
	errHeaders.Set(portforward.PortForwardRequestIDHeader, "1")

	errorStream, err := conn.CreateStream(errHeaders)
	require.NoError(t, err)

	t.Log("Sending data over portforward stream")
	testMsg := "hello portforward\n"
	_, err = dataStream.Write([]byte(testMsg))
	require.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := dataStream.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed reading from stream: %v", err)
	}

	assert.Equal(t, testMsg, string(buf[:n]))
	_ = errorStream.Close()
}

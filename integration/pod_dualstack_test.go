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
	"net"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPodDualStack(t *testing.T) {
	testPodLogDir := t.TempDir()

	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "dualstack", WithPodLogDirectory(testPodLogDir))

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container to print env")
	var command ContainerOpts
	if goruntime.GOOS == "windows" {
		command = WithCommand("ipconfig")
	} else {
		command = WithCommand("ip", "address", "show", "dev", "eth0")
	}
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		command,
		WithLogPath(containerName),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for container to finish running")
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	status, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	ip := status.GetNetwork().GetIp()
	additionalIps := status.GetNetwork().GetAdditionalIps()

	var ipv4Regex, ipv6Regex string
	if goruntime.GOOS == "windows" {
		ipv4Regex = "^\\s*IPv4 Address"
		ipv6Regex = "^\\s*IPv6 Address"
	} else {
		ipv4Regex = "inet .* scope global"
		ipv6Regex = "inet6 .* scope global"
	}

	ipv4Enabled, err := regexp.MatchString(ipv4Regex, string(content))
	assert.NoError(t, err)
	ipv6Enabled, err := regexp.MatchString(ipv6Regex, string(content))
	assert.NoError(t, err)

	if ipv4Enabled && ipv6Enabled {
		t.Log("Dualstack should be enabled")
		require.Len(t, additionalIps, 1)
		assert.NotNil(t, net.ParseIP(ip).To4())
		assert.Nil(t, net.ParseIP(additionalIps[0].GetIp()).To4())
	} else {
		t.Log("Dualstack should not be enabled")
		assert.Len(t, additionalIps, 0)
		assert.NotEmpty(t, ip)
	}
}

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
	"encoding/json"
	"net"
	"os"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPodSandboxWithoutNetwork(t *testing.T) {
	sbConfig := PodSandboxConfig(t.Name(), "network-isolated")
	sbConfig.PodNetwork = runtime.PodNetwork_POD_NETWORK_NONE

	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

	status, info, err := SandboxInfo(sb)
	require.NoError(t, err)
	assert.Empty(t, status.GetNetwork().GetIp())
	assert.Empty(t, status.GetNetwork().GetAdditionalIps())
	assert.Nil(t, info.CNIResult, "network-isolated sandbox must not have a CNI result")

	require.NotNil(t, info.RuntimeSpec.Linux)
	var netNSPath string
	for _, ns := range info.RuntimeSpec.Linux.Namespaces {
		if ns.Type == runtimespec.NetworkNamespace {
			netNSPath = ns.Path
			break
		}
	}
	require.NotEmpty(t, netNSPath, "network-isolated sandbox must have its own network namespace")

	ns, err := netns.GetFromPath(netNSPath)
	require.NoError(t, err)
	defer ns.Close()

	h, err := netlink.NewHandleAt(ns)
	require.NoError(t, err)
	defer h.Close()

	loopback, err := h.LinkByName("lo")
	require.NoError(t, err)
	assert.NotZero(t, loopback.Attrs().Flags&net.FlagUp, "loopback must be brought up")
	_, err = h.LinkByName("eth0")
	assert.Error(t, err, "network-isolated sandbox must not have a CNI-provided eth0")
}

func TestPodSandboxWithoutNetworkSkipsCNI(t *testing.T) {
	sbConfig := PodSandboxConfig(t.Name(), "network-isolated")
	sbConfig.PodNetwork = runtime.PodNetwork_POD_NETWORK_NONE
	conf := &failpointConf{
		Add: "1*error(unexpected-cni-add)",
		Del: "1*error(unexpected-cni-del)",
	}
	injectCNIFailpoint(t, sbConfig, conf)
	assertCNIUnused := func() {
		t.Helper()
		data, err := os.ReadFile(sbConfig.Annotations[failpointCNIConfPathKey])
		require.NoError(t, err)
		var remaining failpointConf
		require.NoError(t, json.Unmarshal(data, &remaining))
		assert.Equal(t, *conf, remaining, "CNI ADD and DEL must not consume any failpoints")
	}

	sb, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	if sb != "" {
		t.Cleanup(func() {
			assert.NoError(t, runtimeService.StopPodSandbox(sb))
			assert.NoError(t, runtimeService.RemovePodSandbox(sb))
		})
	}
	require.NoError(t, err, "isolated sandbox must start without calling CNI ADD")
	assertCNIUnused()

	status, info, err := SandboxInfo(sb)
	require.NoError(t, err)
	assert.Empty(t, status.GetNetwork().GetIp())
	assert.Empty(t, status.GetNetwork().GetAdditionalIps())
	assert.Nil(t, info.CNIResult)

	require.NoError(t, runtimeService.StopPodSandbox(sb))
	// Checking success alone is insufficient: CRI can ignore CNI DEL errors
	// when there is no CNI result. Verify the failpoint was not invoked.
	assertCNIUnused()
	status, err = runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Empty(t, status.GetNetwork().GetIp())
	assert.Empty(t, status.GetNetwork().GetAdditionalIps())

	require.NoError(t, runtimeService.RemovePodSandbox(sb))
	assertCNIUnused()
}

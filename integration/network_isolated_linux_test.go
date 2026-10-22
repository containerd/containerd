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
	sbConfig.DefaultNetwork = runtime.PodSandboxDefaultNetwork_DEFAULT_NETWORK_NONE

	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

	status, info, err := SandboxInfo(sb)
	require.NoError(t, err)
	assert.Empty(t, status.GetNetwork().GetIp())
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

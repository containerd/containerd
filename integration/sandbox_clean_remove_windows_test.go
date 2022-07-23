//go:build windows
// +build windows

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
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestSandboxRemoveWithoutIPLeakage(t *testing.T) {
	t.Logf("Make sure host-local ipam is in use")
	config, err := CRIConfig()
	require.NoError(t, err)
	fs, err := os.ReadDir(config.NetworkPluginConfDir)
	require.NoError(t, err)
	require.NotEmpty(t, fs)
	f := filepath.Join(config.NetworkPluginConfDir, fs[0].Name())
	cniConfig, err := os.ReadFile(f)
	require.NoError(t, err)
	if !strings.Contains(string(cniConfig), "azure-vnet-ipam") {
		t.Skip("azure-vnet ipam is not in use")
	}

	t.Logf("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "remove-without-ip-leakage")
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		// Make sure the sandbox is cleaned up in any case.
		runtimeService.StopPodSandbox(sb)
		runtimeService.RemovePodSandbox(sb)
	}()

	t.Logf("Get pod information")
	status, info, err := SandboxInfo(sb)
	require.NoError(t, err)
	ip := status.GetNetwork().GetIp()
	require.NotEmpty(t, ip)
	require.NotNil(t, info.RuntimeSpec.Windows)
	netNS := info.RuntimeSpec.Windows.Network.NetworkNamespace
	require.NotEmpty(t, netNS, "network namespace should be set")

	t.Logf("Should be able to find the pod ip in host-local checkpoint")
	checkIP := func(ip string) bool {
		f, err := os.Open("azure-vnet-ipam.json")
		require.NoError(t, err)
		defer f.Close()

		data, err := io.ReadAll(f)
		require.NoError(t, err)

		var jsonData map[string]interface{}
		err = json.Unmarshal(data, &jsonData)
		require.NoError(t, err)

		walkJSON := func(initial map[string]interface{}, elementNames ...string) map[string]interface{} {
			element := initial
			for _, name := range elementNames {
				element = element[name].(map[string]interface{})
			}

			return element
		}

		pools := walkJSON(jsonData, "IPAM", "AddressSpaces", "local", "Pools")

		ipAddr := net.ParseIP(ip)
		var ipPool map[string]interface{}
		for poolID, pool := range pools {
			// Each pool will contain its key as its subnet.
			_, ipnet, _ := net.ParseCIDR(poolID)
			if ipnet.Contains(ipAddr) {
				ipPool = pool.(map[string]interface{})
				break
			}
		}

		// Search in the IP Pool and see if it's in use or not.
		for address, details := range walkJSON(ipPool, "Addresses") {
			if address == ip {
				d := details.(map[string]interface{})
				return d["InUse"].(bool)
			}
		}

		return false
	}
	require.True(t, checkIP(ip))

	t.Logf("Kill sandbox container")
	require.NoError(t, KillPid(int(info.Pid)))

	t.Logf("Delete network namespace")
	cmd := exec.Command("hnsdiag.exe", "delete", "namespaces", netNS)
	require.NoError(t, cmd.Run())

	t.Logf("Network namespace should be closed")
	_, info, err = SandboxInfo(sb)
	require.NoError(t, err)
	assert.True(t, info.NetNSClosed)

	t.Logf("Sandbox state should be NOTREADY")
	assert.NoError(t, Eventually(func() (bool, error) {
		status, err := runtimeService.PodSandboxStatus(sb)
		if err != nil {
			return false, err
		}
		return status.GetState() == runtime.PodSandboxState_SANDBOX_NOTREADY, nil
	}, time.Second, 30*time.Second), "sandbox state should become NOTREADY")

	t.Logf("Should still be able to find the pod ip in host-local checkpoint")
	assert.True(t, checkIP(ip))

	t.Logf("Should be able to stop and remove the sandbox")
	assert.NoError(t, runtimeService.StopPodSandbox(sb))
	assert.NoError(t, runtimeService.RemovePodSandbox(sb))

	t.Logf("Should not be able to find the pod ip in host-local checkpoint")
	assert.False(t, checkIP(ip), fmt.Sprintf("The IP: %s is still in use in azure-vnet-ipam.json", ip))
}

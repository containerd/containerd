//go:build windows

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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/hcsshim/osversion"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows/registry"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Returns what nanoserver image version to use according to the build number
func getTestImage() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	b, _, _ := k.GetStringValue("CurrentBuild")
	buildNum, _ := strconv.Atoi(b)

	switch buildNum {
	case osversion.RS1:
		return "ghcr.io/containerd/windows/nanoserver:sac2016", nil
	case osversion.RS3:
		return "ghcr.io/containerd/windows/nanoserver:1709", nil
	case osversion.RS4:
		return "ghcr.io/containerd/windows/nanoserver:1803", nil
	case osversion.RS5:
		return "ghcr.io/containerd/windows/nanoserver:1809", nil
	case osversion.V19H1:
		return "ghcr.io/containerd/windows/nanoserver:1903", nil
	case osversion.V19H2:
		return "ghcr.io/containerd/windows/nanoserver:1909", nil
	case osversion.V20H1:
		return "ghcr.io/containerd/windows/nanoserver:2004", nil
	case osversion.V20H2:
		return "ghcr.io/containerd/windows/nanoserver:20H2", nil
	case osversion.V21H2Server:
		return "ghcr.io/containerd/windows/nanoserver:ltsc2022", nil
	default:
		// Due to some efforts in improving down-level compatibility for Windows containers (see
		// https://techcommunity.microsoft.com/t5/containers/windows-server-2022-and-beyond-for-containers/ba-p/2712487)
		// the ltsc2022 image should continue to work on builds ws2022 and onwards (Windows 11 for example). With this in mind,
		// if there's no mapping for the host build just use the Windows Server 2022 image.
		if buildNum > osversion.V21H2Server {
			return "ghcr.io/containerd/windows/nanoserver:ltsc2022", nil
		}
		return "", fmt.Errorf("No test image defined for Windows build version: %s", b)
	}
}

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

// isNotFoundErr normalizes "not found" variants across implementations.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "notfound")
}

func stopPodSandbox(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, podID string) {
	t.Helper()
	_, err := client.StopPodSandbox(ctx, &runtime.StopPodSandboxRequest{
		PodSandboxId: podID,
	})
	require.NoError(t, err, "failed StopPodSandbox for sandbox: %s", podID)

	// Give it a moment to start the stopping process
	time.Sleep(500 * time.Millisecond)

	// Wait for sandbox to report NOTREADY and its containers to be exited.
	// Windows can take longer to fully quiesce, use a 60s deadline.
	dl := time.Now().Add(60 * time.Second)
	for {
		ps, err := client.PodSandboxStatus(ctx, &runtime.PodSandboxStatusRequest{PodSandboxId: podID})
		if err == nil && ps.Status != nil && ps.Status.GetState() == runtime.PodSandboxState_SANDBOX_NOTREADY {
			break
		}
		if time.Now().After(dl) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	lresp, err := client.ListContainers(ctx, &runtime.ListContainersRequest{
		Filter: &runtime.ContainerFilter{PodSandboxId: podID},
	})
	if err == nil {
		for _, c := range lresp.Containers {
			cs, err := client.ContainerStatus(ctx, &runtime.ContainerStatusRequest{ContainerId: c.Id})
			if err != nil {
				continue
			}
			if cs.Status == nil || cs.Status.GetState() != runtime.ContainerState_CONTAINER_EXITED {
				_, _ = client.StopContainer(ctx, &runtime.StopContainerRequest{ContainerId: c.Id, Timeout: 60})
				dl2 := time.Now().Add(60 * time.Second)
				for {
					cs, err = client.ContainerStatus(ctx, &runtime.ContainerStatusRequest{ContainerId: c.Id})
					if err == nil && cs.Status != nil && cs.Status.GetState() == runtime.ContainerState_CONTAINER_EXITED {
						break
					}
					if time.Now().After(dl2) {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}
}

func stopContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	_, err := client.StopContainer(ctx, &runtime.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     30,
	})
	require.NoError(t, err, "failed StopContainer request for container: %s", containerID)

	// Wait until EXITED to avoid races on subsequent remove.
	dl := time.Now().Add(30 * time.Second)
	for {
		cs, err := client.ContainerStatus(ctx, &runtime.ContainerStatusRequest{ContainerId: containerID})
		if err == nil && cs.Status != nil && cs.Status.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			break
		}
		if time.Now().After(dl) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func startContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	_, err := client.StartContainer(ctx, &runtime.StartContainerRequest{
		ContainerId: containerID,
	})
	require.NoError(t, err, "failed StartContainer request for container: %s", containerID)
}

// Enhanced removeContainer with detailed logging
func removeContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	t.Logf("Attempting to remove container: %s", containerID)

	// Try to fetch current status; if missing, consider it already removed.
	cs, err := client.ContainerStatus(ctx, &runtime.ContainerStatusRequest{ContainerId: containerID})
	if err != nil {
		if isNotFoundErr(err) {
			t.Logf("Container %s already removed", containerID)
			return
		}
		t.Logf("Error getting container status: %v", err)
		require.NoError(t, err, "failed to get container status: %s", containerID)
	}

	state := runtime.ContainerState_CONTAINER_UNKNOWN
	if cs != nil && cs.Status != nil {
		state = cs.Status.GetState()
		t.Logf("Container %s current state: %v", containerID, state)
	}

	// If running, stop and wait for EXITED.
	if state == runtime.ContainerState_CONTAINER_RUNNING {
		t.Logf("Stopping container %s before removal", containerID)
		_, err = client.StopContainer(ctx, &runtime.StopContainerRequest{
			ContainerId: containerID,
			Timeout:     120, // Increased timeout
		})
		// Ignore benign errors like already stopped / not found.
		if err != nil && !isNotFoundErr(err) && !strings.Contains(strings.ToLower(err.Error()), "already") {
			t.Logf("Error stopping container: %v", err)
			require.NoError(t, err, "failed to stop container: %s", containerID)
		}

		dl := time.Now().Add(120 * time.Second) // Increased timeout
		for {
			cs, err = client.ContainerStatus(ctx, &runtime.ContainerStatusRequest{ContainerId: containerID})
			if err != nil {
				t.Logf("Container status error during wait: %v", err)
				break // Possibly removed concurrently.
			}
			if cs.Status != nil && cs.Status.GetState() == runtime.ContainerState_CONTAINER_EXITED {
				t.Logf("Container %s successfully exited", containerID)
				break
			}
			if time.Now().After(dl) {
				currentState := "nil"
				if cs != nil && cs.Status != nil {
					currentState = cs.Status.GetState().String()
				}
				t.Logf("Timeout waiting for container %s to exit (current state: %v)", containerID, currentState)
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Attempt removal.
	t.Logf("Attempting final removal of container: %s", containerID)
	_, err = client.RemoveContainer(ctx, &runtime.RemoveContainerRequest{ContainerId: containerID})
	if err != nil {
		t.Logf("First removal attempt failed: %v", err)
		// If still running due to a race, force a final stop and retry once.
		if strings.Contains(strings.ToLower(err.Error()), "still running") {
			t.Logf("Container %s still running, forcing stop and retrying removal", containerID)
			_, _ = client.StopContainer(ctx, &runtime.StopContainerRequest{ContainerId: containerID, Timeout: 30})
			time.Sleep(5 * time.Second) // Increased wait time
			_, err = client.RemoveContainer(ctx, &runtime.RemoveContainerRequest{ContainerId: containerID})
		}
		// If already removed, treat as success.
		if isNotFoundErr(err) {
			t.Logf("Container %s already removed", containerID)
			return
		}
	}
	require.NoError(t, err, "failed to remove container: %s", containerID)
	t.Logf("Successfully removed container: %s", containerID)
}

func removePodSandbox(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, podID string) {
	t.Helper()

	// Stop first so its containers transition to EXITED before removal.
	stopPodSandbox(ctx, t, client, podID)

	// Then remove. NotFound is benign.
	_, err := client.RemovePodSandbox(ctx, &runtime.RemovePodSandboxRequest{
		PodSandboxId: podID,
	})
	if err != nil && isNotFoundErr(err) {
		t.Logf("Pod sandbox %s already removed", podID)
		return
	}
	require.NoError(t, err, "failed RemovePodSandbox for sandbox: %s", podID)
}

// This test checks if create/stop and remove pods and containers work as expected
func TestCreateContainer(t *testing.T) {
	testImage, err := getTestImage()
	if err != nil {
		t.Skip("skipping test, error: ", err)
	}
	client, err := RawRuntimeClient()
	require.NoError(t, err, "failed to get raw grpc runtime service client")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })

	t.Log("Create a pod sandbox")
	sbConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name: t.Name(),
		},
	}
	sandboxRequest := &runtime.RunPodSandboxRequest{
		Config:         sbConfig,
		RuntimeHandler: "runhcs-wcow-process",
	}
	sandBoxResponse, err := client.RunPodSandbox(ctx, sandboxRequest)
	require.NoError(t, err, "failed RunPodSandbox request")
	// Make sure the sandbox is cleaned up.
	t.Cleanup(func() { removePodSandbox(ctx, t, client, sandBoxResponse.PodSandboxId) })

	EnsureImageExists(t, testImage)

	t.Log("Create a container")
	createCtrRequest := &runtime.CreateContainerRequest{
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name: t.Name() + "-CreateContainerTest",
			},
			Image: &runtime.ImageSpec{
				Image: testImage,
			},
			Command: []string{
				"cmd",
				"/c",
				"ping",
				"-t",
				"127.0.0.1",
			},
			Windows: &runtime.WindowsContainerConfig{
				Resources: &runtime.WindowsContainerResources{
					CpuShares: 500,
				},
			},
		},
		PodSandboxId:  sandBoxResponse.PodSandboxId,
		SandboxConfig: sandboxRequest.Config,
	}

	createCtrResponse, err := client.CreateContainer(ctx, createCtrRequest)
	require.NoError(t, err, "failed CreateContainer request in sandbox: %s", sandBoxResponse.PodSandboxId)
	// Make sure the container is cleaned up.
	t.Cleanup(func() { removeContainer(ctx, t, client, createCtrResponse.ContainerId) })

	startContainer(ctx, t, client, createCtrResponse.ContainerId)
	stopContainer(ctx, t, client, createCtrResponse.ContainerId)
}

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
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/Microsoft/hcsshim/osversion"
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
		return "mcr.microsoft.com/windows/nanoserver:sac2016", nil
	case osversion.RS3:
		return "mcr.microsoft.com/windows/nanoserver:1709", nil
	case osversion.RS4:
		return "mcr.microsoft.com/windows/nanoserver:1803", nil
	case osversion.RS5:
		return "mcr.microsoft.com/windows/nanoserver:1809", nil
	case osversion.V19H1:
		return "mcr.microsoft.com/windows/nanoserver:1903", nil
	case osversion.V19H2:
		return "mcr.microsoft.com/windows/nanoserver:1909", nil
	case osversion.V20H1:
		return "mcr.microsoft.com/windows/nanoserver:2004", nil
	case osversion.V20H2:
		return "mcr.microsoft.com/windows/nanoserver:20H2", nil
	case osversion.V21H2Server:
		return "mcr.microsoft.com/windows/nanoserver:ltsc2022", nil
	default:
		// Due to some efforts in improving down-level compatibility for Windows containers (see
		// https://techcommunity.microsoft.com/t5/containers/windows-server-2022-and-beyond-for-containers/ba-p/2712487)
		// the ltsc2022 image should continue to work on builds ws2022 and onwards (Windows 11 for example). With this in mind,
		// if there's no mapping for the host build just use the Windows Server 2022 image.
		if buildNum > osversion.V21H2Server {
			return "mcr.microsoft.com/windows/nanoserver:ltsc2022", nil
		}
		return "", fmt.Errorf("No test image defined for Windows build version: %s", b)
	}
}

func removePodSandbox(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, podID string) {
	t.Helper()
	_, err := client.RemovePodSandbox(ctx, &runtime.RemovePodSandboxRequest{
		PodSandboxId: podID,
	})
	require.NoError(t, err, "failed RemovePodSandbox for sandbox: %s", podID)
}

func stopPodSandbox(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, podID string) {
	t.Helper()
	_, err := client.StopPodSandbox(ctx, &runtime.StopPodSandboxRequest{
		PodSandboxId: podID,
	})
	require.NoError(t, err, "failed StopPodSandbox for sandbox: %s", podID)
}

func stopContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	_, err := client.StopContainer(ctx, &runtime.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     0,
	})
	require.NoError(t, err, "failed StopContainer request for container: %s", containerID)
}

func startContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	_, err := client.StartContainer(ctx, &runtime.StartContainerRequest{
		ContainerId: containerID,
	})
	require.NoError(t, err, "failed StartContainer request for container: %s", containerID)
}

func removeContainer(ctx context.Context, t *testing.T, client runtime.RuntimeServiceClient, containerID string) {
	t.Helper()
	_, err := client.RemoveContainer(ctx, &runtime.RemoveContainerRequest{
		ContainerId: containerID,
	})
	require.NoError(t, err, "failed RemoveContainer request for container: %s", containerID)
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
	t.Cleanup(func() { stopPodSandbox(ctx, t, client, sandBoxResponse.PodSandboxId) })

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

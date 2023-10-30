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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestWindowsDevice(t *testing.T) {
	testPodLogDir := t.TempDir()

	t.Log("Create a sandbox with log directory")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "windows-device",
		WithPodLogDirectory(testPodLogDir),
	)

	var (
		// TODO: An image with the device-dumper
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	const (
		// Mount this device class to expose host-GPU-accelerated DirectX inside the container
		// https://techcommunity.microsoft.com/t5/containers/bringing-gpu-acceleration-to-windows-containers/ba-p/393939
		GUID_DEVINTERFACE_DISPLAY_ADAPTER = "5B45201D-F2F2-4F3B-85BB-30FF1F953599" //nolint:revive
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container to run the device test")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		// Per C:\windows\System32\containers\devices.def, enabling GUID_DEVINTERFACE_DISPLAY_ADAPTER
		// will mount the host driver store into the container at this location.
		WithCommand("sh", "-c", "ls -d /Windows/System32/HostDriverStore/* | grep /Windows/System32/HostDriverStore/FileRepository"),
		WithLogPath(containerName),
		WithDevice("", "class/"+GUID_DEVINTERFACE_DISPLAY_ADAPTER, ""),
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

	t.Log("Check container log")
	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagFull, "/Windows/System32/HostDriverStore/FileRepository"),
	})
}

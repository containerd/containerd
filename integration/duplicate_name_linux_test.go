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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type versionedStatus struct {
	// Version indicates the version of the versioned container status.
	Version string
	container.Status
}

// https://github.com/containerd/containerd/issues/7247
func TestDuplicateNameIssue7247(t *testing.T) {
	t.Logf("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "duplicate-name7247")
	t.Logf("Create the sandbox again should fail")
	_, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.Error(t, err)
	pauseImage := images.Get(images.Pause)
	EnsureImageExists(t, pauseImage)
	t.Logf("Create a container")
	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)
	EnsureImageExists(t, testImage)
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 1000"),
	)
	cID, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	err = runtimeService.StartContainer(cID)
	require.NoError(t, err)
	status, err := runtimeService.ContainerStatus(cID)
	require.NoError(t, err)
	assert.Equal(t, status.GetState(), runtime.ContainerState_CONTAINER_RUNNING)
	cfg := criRuntimeInfo(t, runtimeService)
	rootDir := cfg["rootDir"].(string)
	containerDir := filepath.Join(rootDir, "containers", cID)
	statusDir := filepath.Join(containerDir, "status")
	versioned := &versionedStatus{}
	data, err := os.ReadFile(statusDir)
	require.NoError(t, err)
	err = json.Unmarshal(data, versioned)
	require.NoError(t, err)
	pid := strconv.Itoa(int(versioned.Pid))
	// make status file can't be changed
	_, err = exec.Command("chattr", "+i", statusDir).CombinedOutput()
	require.NoError(t, err)
	StopContainerd(t, syscall.SIGTERM)

	// kill pid to make sure the container status changed
	_, err = exec.Command("kill", "-9", pid).CombinedOutput()
	require.NoError(t, err)
	// if can't update status info status file, containerd won't start.
	StartContainerdFailed(t)
	// make status file can  be changed.
	_, err = exec.Command("chattr", "-i", statusDir).CombinedOutput()
	require.NoError(t, err)
	StartContainerd(t)
	t.Logf("Create the container again should fail")
	_, err = runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.Error(t, err)
}

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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	containerUserName = "ContainerUser"
	// containerUserSID is a well known SID that is set on the
	// ContainerUser username inside a Windows container.
	containerUserSID = "S-1-5-93-2-2"
)

func TestVolumeCopyUp(t *testing.T) {
	var (
		testImage   = GetImage(VolumeCopyUp)
		execTimeout = time.Minute
	)

	t.Logf("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "volume-copy-up")

	EnsureImageExists(t, testImage)

	t.Logf("Create a container with volume-copy-up test image")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		WithCommand("sleep", "150"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Logf("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	// ghcr.io/containerd/volume-copy-up:2.1 contains a test_dir
	// volume, which contains a test_file with content "test_content".
	t.Logf("Check whether volume contains the test file")
	stdout, stderr, err := runtimeService.ExecSync(cn, []string{
		"cat",
		"/test_dir/test_file",
	}, execTimeout)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, "test_content\n", string(stdout))

	t.Logf("Check host path of the volume")
	volumePaths, err := getHostPathForVolumes(*criRoot, cn)
	require.NoError(t, err)
	assert.Equal(t, len(volumePaths), 1, "expected exactly 1 volume")

	testFilePath := filepath.Join(volumePaths[0], "test_file")
	contents, err := os.ReadFile(testFilePath)
	require.NoError(t, err)
	assert.Equal(t, "test_content\n", string(contents))

	t.Logf("Update volume from inside the container")
	_, _, err = runtimeService.ExecSync(cn, []string{
		"sh",
		"-c",
		"echo new_content > /test_dir/test_file",
	}, execTimeout)
	require.NoError(t, err)

	t.Logf("Check whether host path of the volume is updated")
	contents, err = os.ReadFile(testFilePath)
	require.NoError(t, err)
	assert.Equal(t, "new_content\n", string(contents))
}

func TestVolumeOwnership(t *testing.T) {
	var (
		testImage   = GetImage(VolumeOwnership)
		execTimeout = time.Minute
	)

	t.Logf("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "volume-ownership")

	EnsureImageExists(t, testImage)

	t.Logf("Create a container with volume-ownership test image")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		WithCommand("sleep", "150"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Logf("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	// ghcr.io/containerd/volume-ownership:2.1 contains a test_dir
	// volume, which is owned by nobody:nogroup.
	// On Windows, the folder is situated in C:\volumes\test_dir and is owned
	// by ContainerUser (SID: S-1-5-93-2-2). A helper tool get_owner.exe should
	// exist inside the container that returns the owner in the form of USERNAME:SID.
	t.Logf("Check ownership of test directory inside container")

	cmd := []string{
		"stat", "-c", "%U:%G", "/test_dir",
	}
	expectedContainerOutput := "nobody:nogroup\n"
	expectedHostOutput := "nobody:nogroup\n"
	if goruntime.GOOS == "windows" {
		cmd = []string{
			"C:\\bin\\get_owner.exe",
			"C:\\volumes\\test_dir",
		}
		expectedContainerOutput = fmt.Sprintf("%s:%s", containerUserName, containerUserSID)
		// The username is unknown on the host, but we can still get the SID.
		expectedHostOutput = containerUserSID
	}
	stdout, stderr, err := runtimeService.ExecSync(cn, cmd, execTimeout)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, expectedContainerOutput, string(stdout))

	t.Logf("Check ownership of test directory on the host")
	volumePaths, err := getHostPathForVolumes(*criRoot, cn)
	require.NoError(t, err)
	assert.Equal(t, len(volumePaths), 1, "expected exactly 1 volume")

	output, err := getOwnership(volumePaths[0])
	require.NoError(t, err)
	assert.Equal(t, expectedHostOutput, output)
}

func getHostPathForVolumes(criRoot, containerID string) ([]string, error) {
	hostPath := filepath.Join(criRoot, "containers", containerID, "volumes")
	if _, err := os.Stat(hostPath); err != nil {
		return nil, err
	}

	volumes, err := os.ReadDir(hostPath)
	if err != nil {
		return nil, err
	}

	if len(volumes) == 0 {
		return []string{}, nil
	}

	volumePaths := make([]string, len(volumes))
	for idx, volume := range volumes {
		volumePaths[idx] = filepath.Join(hostPath, volume.Name())
	}

	return volumePaths, nil
}

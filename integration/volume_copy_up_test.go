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
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	containerUserName = "ContainerUser"
	// containerUserSID is a well known SID that is set on the
	// ContainerUser username inside a Windows container.
	containerUserSID = "S-1-5-93-2-2"
)

type volumeFile struct {
	fileName string
	contents string
}

type containerVolume struct {
	containerPath string
	files         []volumeFile
}

func TestVolumeCopyUp(t *testing.T) {
	var (
		testImage   = images.Get(images.VolumeCopyUp)
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

	expectedVolumes := []containerVolume{
		{
			containerPath: "/test_dir",
			files: []volumeFile{
				{
					fileName: "test_file",
					contents: "test_content\n",
				},
			},
		},
		{
			containerPath: "/:colon_prefixed",
			files: []volumeFile{
				{
					fileName: "colon_prefixed_file",
					contents: "test_content\n",
				},
			},
		},
		{
			containerPath: "/C:/weird_test_dir",
			files: []volumeFile{
				{
					fileName: "weird_test_file",
					contents: "test_content\n",
				},
			},
		},
	}

	if runtime.GOOS == "windows" {
		expectedVolumes = []containerVolume{
			{
				containerPath: "C:\\test_dir",
				files: []volumeFile{
					{
						fileName: "test_file",
						contents: "test_content\n",
					},
				},
			},
			{
				containerPath: "D:",
				files:         []volumeFile{},
			},
		}
	}

	volumeMappings, err := getContainerBindVolumes(t, cn)
	require.NoError(t, err)

	t.Logf("Check host path of the volume")
	for _, vol := range expectedVolumes {
		_, ok := volumeMappings[vol.containerPath]
		assert.Equalf(t, true, ok, "expected to find volume %s", vol.containerPath)
	}

	// ghcr.io/containerd/volume-copy-up:2.2 contains 3 volumes on Linux and 2 volumes on Windows.
	// On linux, each of the volumes contains a single file, all with the same conrent. On Windows,
	// non C volumes defined in the image start out as empty.
	for _, vol := range expectedVolumes {
		files, err := os.ReadDir(volumeMappings[vol.containerPath])
		require.NoError(t, err)
		assert.Equal(t, len(vol.files), len(files))

		for _, file := range vol.files {
			t.Logf("Check whether volume %s contains the test file %s", vol.containerPath, file.fileName)
			stdout, stderr, err := runtimeService.ExecSync(cn, []string{
				"cat",
				filepath.ToSlash(filepath.Join(vol.containerPath, file.fileName)),
			}, execTimeout)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, file.contents, string(stdout))
		}
	}

	testFilePath := filepath.Join(volumeMappings[expectedVolumes[0].containerPath], expectedVolumes[0].files[0].fileName)
	inContainerPath := filepath.Join(expectedVolumes[0].containerPath, expectedVolumes[0].files[0].fileName)
	contents, err := os.ReadFile(testFilePath)
	require.NoError(t, err)
	assert.Equal(t, "test_content\n", string(contents))

	t.Logf("Update volume from inside the container")
	_, _, err = runtimeService.ExecSync(cn, []string{
		"sh",
		"-c",
		fmt.Sprintf("echo new_content > %s", filepath.ToSlash(inContainerPath)),
	}, execTimeout)
	require.NoError(t, err)

	t.Logf("Check whether host path of the volume is updated")
	contents, err = os.ReadFile(testFilePath)
	require.NoError(t, err)
	assert.Equal(t, "new_content\n", string(contents))
}

func TestVolumeOwnership(t *testing.T) {
	var (
		testImage   = images.Get(images.VolumeOwnership)
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
	// volume, which is owned by 65534:65534 (nobody:nogroup, or nobody:nobody).
	// On Windows, the folder is situated in C:\volumes\test_dir and is owned
	// by ContainerUser (SID: S-1-5-93-2-2). A helper tool get_owner.exe should
	// exist inside the container that returns the owner in the form of USERNAME:SID.
	t.Logf("Check ownership of test directory inside container")

	volumePath := "/test_dir"
	cmd := []string{
		"stat", "-c", "%u:%g", volumePath,
	}
	expectedContainerOutput := "65534:65534\n"
	expectedHostOutput := "65534:65534\n"
	if runtime.GOOS == "windows" {
		volumePath = "C:\\volumes\\test_dir"
		cmd = []string{
			"C:\\bin\\get_owner.exe",
			volumePath,
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
	volumePaths, err := getContainerBindVolumes(t, cn)
	require.NoError(t, err)

	output, err := getOwnership(volumePaths[volumePath])
	require.NoError(t, err)
	assert.Equal(t, expectedHostOutput, output)
}

func getContainerBindVolumes(t *testing.T, containerID string) (map[string]string, error) {
	client, err := RawRuntimeClient()
	require.NoError(t, err, "failed to get raw grpc runtime service client")
	request := &v1.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	}
	response, err := client.ContainerStatus(context.TODO(), request)
	require.NoError(t, err)
	ret := make(map[string]string)

	mounts := struct {
		RuntimeSpec struct {
			Mounts []specs.Mount `json:"mounts"`
		} `json:"runtimeSpec"`
	}{}

	info := response.Info["info"]
	err = json.Unmarshal([]byte(info), &mounts)
	require.NoError(t, err)
	for _, mount := range mounts.RuntimeSpec.Mounts {
		ret[mount.Destination] = mount.Source
	}
	return ret, nil
}

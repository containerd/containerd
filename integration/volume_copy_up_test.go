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
	"os/exec"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolumeCopyUp(t *testing.T) {
	if goruntime.GOOS == "windows" {
		// TODO(claudiub): Remove this when the volume-copy-up image has Windows support.
		// https://github.com/containerd/containerd/pull/5162
		t.Skip("Skipped on Windows.")
	}
	var (
		testImage   = GetImage(VolumeCopyUp)
		execTimeout = time.Minute
	)

	t.Logf("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "volume-copy-up")
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

	EnsureImageExists(t, testImage)

	t.Logf("Create a container with volume-copy-up test image")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		WithCommand("tail", "-f", "/dev/null"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Logf("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	// gcr.io/k8s-cri-containerd/volume-copy-up:2.0 contains a test_dir
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
	hostCmd := fmt.Sprintf("find %s/containers/%s/volumes/*/test_file | xargs cat", *criRoot, cn)
	output, err := exec.Command("sh", "-c", hostCmd).CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "test_content\n", string(output))

	t.Logf("Update volume from inside the container")
	_, _, err = runtimeService.ExecSync(cn, []string{
		"sh",
		"-c",
		"echo new_content > /test_dir/test_file",
	}, execTimeout)
	require.NoError(t, err)

	t.Logf("Check whether host path of the volume is updated")
	output, err = exec.Command("sh", "-c", hostCmd).CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "new_content\n", string(output))
}

func TestVolumeOwnership(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipped on Windows.")
	}
	var (
		testImage   = GetImage(VolumeOwnership)
		execTimeout = time.Minute
	)

	t.Logf("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "volume-ownership")
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

	EnsureImageExists(t, testImage)

	t.Logf("Create a container with volume-ownership test image")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		WithCommand("tail", "-f", "/dev/null"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Logf("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	// gcr.io/k8s-cri-containerd/volume-ownership:2.0 contains a test_dir
	// volume, which is owned by nobody:nogroup.
	t.Logf("Check ownership of test directory inside container")
	stdout, stderr, err := runtimeService.ExecSync(cn, []string{
		"stat", "-c", "%U:%G", "/test_dir",
	}, execTimeout)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, "nobody:nogroup\n", string(stdout))

	t.Logf("Check ownership of test directory on the host")
	hostCmd := fmt.Sprintf("find %s/containers/%s/volumes/* | xargs stat -c %%U:%%G", *criRoot, cn)
	output, err := exec.Command("sh", "-c", hostCmd).CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, "nobody:nogroup\n", string(output))
}

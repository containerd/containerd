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
	goruntime "runtime"
	"testing"

	"github.com/containerd/containerd/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func genTruncIndex(normalName string) string {
	return normalName[:(len(normalName)+1)/2]
}

func TestTruncIndex(t *testing.T) {
	sbConfig := PodSandboxConfig("sandbox", "truncindex")

	t.Logf("Pull an image")
	var appImage = images.Get(images.BusyBox)

	imgID := EnsureImageExists(t, appImage)
	imgTruncID := genTruncIndex(imgID)

	t.Logf("Get image status by truncindex, truncID: %s", imgTruncID)
	res, err := imageService.ImageStatus(&runtimeapi.ImageSpec{Image: imgTruncID})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, imgID, res.Id)

	// TODO(yanxuean): for failure test case where there are two images with the same truncindex.
	// if you add n images at least two will share the same leading digit.
	// "sha256:n" where n is the a number from 0-9 where two images have the same trunc,
	// for example sha256:9
	// https://github.com/containerd/cri/pull/352
	// I am thinking how I get the two image which have same trunc.

	// TODO(yanxuean): add test case for ListImages

	t.Logf("Create a sandbox")
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	sbTruncIndex := genTruncIndex(sb)
	var hasStoppedSandbox bool
	defer func() {
		// The 2th StopPodSandbox will fail, the 2th RemovePodSandbox will success.
		if !hasStoppedSandbox {
			assert.NoError(t, runtimeService.StopPodSandbox(sbTruncIndex))
		}
		assert.NoError(t, runtimeService.RemovePodSandbox(sbTruncIndex))
	}()

	t.Logf("Get sandbox status by truncindex")
	sbStatus, err := runtimeService.PodSandboxStatus(sbTruncIndex)
	require.NoError(t, err)
	assert.Equal(t, sb, sbStatus.Id)

	t.Logf("Forward port for sandbox by truncindex")
	_, err = runtimeService.PortForward(&runtimeapi.PortForwardRequest{PodSandboxId: sbTruncIndex, Port: []int32{80}})
	assert.NoError(t, err)

	// TODO(yanxuean): add test case for ListPodSandbox

	t.Logf("Create a container")
	cnConfig := ContainerConfig(
		"containerTruncIndex",
		appImage,
		WithCommand("sleep", "300"),
	)
	cn, err := runtimeService.CreateContainer(sbTruncIndex, cnConfig, sbConfig)
	require.NoError(t, err)
	cnTruncIndex := genTruncIndex(cn)
	defer func() {
		// the 2th RemovePodSandbox will success.
		assert.NoError(t, runtimeService.RemoveContainer(cnTruncIndex))
	}()

	t.Logf("Get container status by truncindex")
	cStatus, err := runtimeService.ContainerStatus(cnTruncIndex)
	require.NoError(t, err)
	assert.Equal(t, cn, cStatus.Id)

	t.Logf("Start the container")
	require.NoError(t, runtimeService.StartContainer(cnTruncIndex))
	var hasStoppedContainer bool
	defer func() {
		// The 2th StopPodSandbox will fail
		if !hasStoppedContainer {
			assert.NoError(t, runtimeService.StopContainer(cnTruncIndex, 10))
		}
	}()

	t.Logf("Stats the container")
	cStats, err := runtimeService.ContainerStats(cnTruncIndex)
	require.NoError(t, err)
	assert.Equal(t, cn, cStats.Attributes.Id)

	t.Logf("Update container memory limit after started")
	if goruntime.GOOS != "windows" {
		err = runtimeService.UpdateContainerResources(cnTruncIndex, &runtimeapi.LinuxContainerResources{
			MemoryLimitInBytes: 50 * 1024 * 1024,
		}, nil)
		assert.NoError(t, err)
	} else {
		err = runtimeService.UpdateContainerResources(cnTruncIndex, nil, &runtimeapi.WindowsContainerResources{
			MemoryLimitInBytes: 50 * 1024 * 1024,
		})
		assert.NoError(t, err)
	}

	t.Logf("Execute cmd in container")
	execReq := &runtimeapi.ExecRequest{
		ContainerId: cnTruncIndex,
		Cmd:         []string{"pwd"},
		Stdout:      true,
	}
	_, err = runtimeService.Exec(execReq)
	assert.NoError(t, err)

	t.Logf("Execute cmd in container by sync")
	_, _, err = runtimeService.ExecSync(cnTruncIndex, []string{"pwd"}, 10)
	assert.NoError(t, err)

	// TODO(yanxuean): add test case for ListContainers

	t.Logf("Get a non exist container status by truncindex")
	err = runtimeService.StopContainer(cnTruncIndex, 10)
	assert.NoError(t, err)
	if err == nil {
		hasStoppedContainer = true
	}
	_, err = runtimeService.ContainerStats(cnTruncIndex)
	assert.Error(t, err)
	assert.NoError(t, runtimeService.RemoveContainer(cnTruncIndex))
	_, err = runtimeService.ContainerStatus(cnTruncIndex)
	assert.Error(t, err)

	t.Logf("Get a non exist sandbox status by truncindex")
	err = runtimeService.StopPodSandbox(sbTruncIndex)
	assert.NoError(t, err)
	if err == nil {
		hasStoppedSandbox = true
	}
	assert.NoError(t, runtimeService.RemovePodSandbox(sbTruncIndex))
	_, err = runtimeService.PodSandboxStatus(sbTruncIndex)
	assert.Error(t, err)
}

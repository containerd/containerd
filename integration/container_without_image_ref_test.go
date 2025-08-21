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
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Test container lifecycle can work without image references.
func TestContainerLifecycleWithoutImageRef(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-lifecycle-without-image-ref")

	testImage := images.Get(images.BusyBox)
	containerName := "test-container"

	img := EnsureImageExists(t, testImage)

	t.Log("Create test container")
	cnConfig := ContainerConfig(containerName, testImage, WithCommand("sleep", "1000"))
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Remove test image")
	assert.NoError(t, imageService.RemoveImage(&runtimev1.ImageSpec{Image: img}))

	t.Log("Container status should be running")
	status, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtimev1.ContainerState_CONTAINER_RUNNING, status.GetState())

	t.Log("Stop container")
	stopTimeout := int64(10)
	if runtime.GOOS == "windows" {
		stopTimeout = 30
	}
	assert.NoError(t, runtimeService.StopContainer(cn, stopTimeout))

	// Wait until the shim reports EXITED to avoid “still running” on sandbox removal.
	waitMax := 10 * time.Second
	if runtime.GOOS == "windows" {
		waitMax = 30 * time.Second
	}
	deadline := time.Now().Add(waitMax)

	for {
		status, err = runtimeService.ContainerStatus(cn)
		require.NoError(t, err)
		if status.GetState() == runtimev1.ContainerState_CONTAINER_EXITED {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Re-check once more and assert exited.
	status, err = runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtimev1.ContainerState_CONTAINER_EXITED, status.GetState())
}

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
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Test container lifecycle can work without image references.
func TestContainerLifecycleWithoutImageRef(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-lifecycle-without-image-ref")

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	img := EnsureImageExists(t, testImage)

	t.Log("Create test container")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sleep", "1000"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Remove test image")
	assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: img}))

	t.Log("Container status should be running")
	status, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, status.GetState())

	t.Logf("Stop container")
	err = runtimeService.StopContainer(cn, 1)
	assert.NoError(t, err)

	t.Log("Container status should be exited")
	status, err = runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.GetState())
}

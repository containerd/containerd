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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestSharedPidMultiProcessContainerStop(t *testing.T) {
	for name, sbConfig := range map[string]*runtime.PodSandboxConfig{
		"hostpid": PodSandboxConfig("sandbox", "host-pid-container-stop", WithHostPid),
		"podpid":  PodSandboxConfig("sandbox", "pod-pid-container-stop", WithPodPid),
	} {
		t.Run(name, func(t *testing.T) {
			t.Log("Create a shared pid sandbox")
			sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, runtimeService.StopPodSandbox(sb))
				assert.NoError(t, runtimeService.RemovePodSandbox(sb))
			}()

			var (
				testImage     = images.Get(images.BusyBox)
				containerName = "test-container"
			)

			EnsureImageExists(t, testImage)

			t.Log("Create a multi-process container")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("sh", "-c", "sleep 10000 & sleep 10000"),
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)

			t.Log("Start the container")
			require.NoError(t, runtimeService.StartContainer(cn))

			t.Log("Stop the container")
			require.NoError(t, runtimeService.StopContainer(cn, 0))

			t.Log("The container state should be exited")
			s, err := runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, s.GetState())
		})
	}
}

func TestContainerStopCancellation(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipped on Windows.")
	}
	t.Log("Create a pod sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "cancel-container-stop")

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container which traps sigterm")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", `trap "echo ignore sigterm" TERM; sleep 1000`),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Stop the container with 3s timeout, but 1s context timeout")
	// Note that with container pid namespace, the sleep process
	// is pid 1, and SIGTERM sent by `StopContainer` will be ignored.
	rawClient, err := RawRuntimeClient()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, err = rawClient.StopContainer(ctx, &runtime.StopContainerRequest{
		ContainerId: cn,
		Timeout:     3,
	})
	assert.Error(t, err)

	t.Log("The container should still be running even after 5 seconds")
	assert.NoError(t, Consistently(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		return s.GetState() == runtime.ContainerState_CONTAINER_RUNNING, nil
	}, 100*time.Millisecond, 5*time.Second))

	t.Log("Stop the container with 1s timeout, without shorter context timeout")
	assert.NoError(t, runtimeService.StopContainer(cn, 1))

	t.Log("The container state should be exited")
	s, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, s.GetState())
}

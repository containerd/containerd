/*
Copyright 2019 The containerd Authors.

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
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

			const (
				testImage     = "busybox"
				containerName = "test-container"
			)
			t.Logf("Pull test image %q", testImage)
			img, err := imageService.PullImage(&runtime.ImageSpec{Image: testImage}, nil)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: img}))
			}()

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
			assert.Equal(t, s.GetState(), runtime.ContainerState_CONTAINER_EXITED)
		})
	}
}

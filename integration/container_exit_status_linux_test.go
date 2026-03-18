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
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestShortLivedContainerPreservesExitCode(t *testing.T) {
	const attempts = 5

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	for i := range attempts {
		t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "short-lived-exit-status")
			containerName := fmt.Sprintf("false-container-%d", i)
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("/bin/false"),
			)

			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)
			require.NoError(t, runtimeService.StartContainer(cn))

			status := waitForContainerState(t, cn, runtime.ContainerState_CONTAINER_EXITED, 30*time.Second)
			require.EqualValues(t, 1, status.GetExitCode(), "short-lived /bin/false should keep exit code 1")

			status, err = runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			require.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.GetState())
			require.EqualValues(t, 1, status.GetExitCode(), "exit code should remain stable across repeated status reads")
		})
	}
}

func waitForContainerState(t *testing.T, containerID string, expected runtime.ContainerState, timeout time.Duration) *runtime.ContainerStatus {
	t.Helper()

	var latest *runtime.ContainerStatus
	require.NoError(t, Eventually(func() (bool, error) {
		status, err := runtimeService.ContainerStatus(containerID)
		if err != nil {
			return false, err
		}
		latest = status
		return status.GetState() == expected, nil
	}, time.Second, timeout))
	return latest
}

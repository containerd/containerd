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

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerStopSignals(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipped on Windows.")
	}

	testCases := []struct {
		name           string
		stopSignal     runtime.Signal
		expectedSignal runtime.Signal
		trapCommand    string
	}{
		{
			name:           "SIGUSR1 stop signal",
			stopSignal:     runtime.Signal_SIGUSR1,
			expectedSignal: runtime.Signal_SIGUSR1,
			trapCommand:    `trap "echo 'Received SIGUSR1'; exit 0" USR1; sleep 5000`,
		},
		{
			name:           "SIGUSR2 stop signal",
			stopSignal:     runtime.Signal_SIGUSR2,
			expectedSignal: runtime.Signal_SIGUSR2,
			trapCommand:    `trap "echo 'Received SIGUSR2'; exit 0" USR2; sleep 5000`,
		},
		{
			name:           "SIGHUP stop signal",
			stopSignal:     runtime.Signal_SIGHUP,
			expectedSignal: runtime.Signal_SIGHUP,
			trapCommand:    `trap "echo 'Received SIGHUP'; exit 0" HUP; sleep 5000`,
		},
		{
			name:           "SIGINT stop signal",
			stopSignal:     runtime.Signal_SIGINT,
			expectedSignal: runtime.Signal_SIGINT,
			trapCommand:    `trap "echo 'Received SIGINT'; exit 0" INT; sleep 5000`,
		},
		{
			name:           "SIGTERM stop signal (default)",
			stopSignal:     runtime.Signal_SIGTERM,
			expectedSignal: runtime.Signal_SIGTERM,
			trapCommand:    `trap "echo 'Received SIGTERM'; exit 0" TERM; sleep 5000`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log("Create a pod sandbox")
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "custom-stop-signal-"+tc.name)

			var (
				testImage     = images.Get(images.BusyBox)
				containerName = "test-container"
			)

			EnsureImageExists(t, testImage)

			t.Log("Create a container with custom stop signal")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("sh", "-c", tc.trapCommand),
				WithStopSignal(tc.stopSignal),
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, runtimeService.RemoveContainer(cn))
			}()

			require.NoError(t, runtimeService.StartContainer(cn))

			status, err := runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedSignal, status.GetStopSignal(), "Container status should return the configured stop signal")
			assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, status.GetState())

			require.NoError(t, runtimeService.StopContainer(cn, 10))

			status, err = runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.GetState())
		})
	}
}

func TestDefaultContainerStopSignal(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipped on Windows.")
	}

	t.Logf("Create a pod config")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "default-stop-signal")

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container without custom stop signal")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 1000"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	require.NoError(t, runtimeService.StartContainer(cn))

	status, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)

	assert.Equal(t, runtime.Signal_SIGTERM, status.GetStopSignal())

	require.NoError(t, runtimeService.StopContainer(cn, 10))

	status, err = runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.GetState())
}

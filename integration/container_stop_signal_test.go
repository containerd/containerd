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
		trapSignal     string
	}{
		{
			name:           "SIGUSR1 stop signal",
			stopSignal:     runtime.Signal_SIGUSR1,
			expectedSignal: runtime.Signal_SIGUSR1,
			trapSignal:     "USR1",
		},
		{
			name:           "SIGUSR2 stop signal",
			stopSignal:     runtime.Signal_SIGUSR2,
			expectedSignal: runtime.Signal_SIGUSR2,
			trapSignal:     "USR2",
		},
		{
			name:           "SIGHUP stop signal",
			stopSignal:     runtime.Signal_SIGHUP,
			expectedSignal: runtime.Signal_SIGHUP,
			trapSignal:     "HUP",
		},
		{
			name:           "SIGINT stop signal",
			stopSignal:     runtime.Signal_SIGINT,
			expectedSignal: runtime.Signal_SIGINT,
			trapSignal:     "INT",
		},
		{
			name:           "SIGTERM stop signal (default)",
			stopSignal:     runtime.Signal_SIGTERM,
			expectedSignal: runtime.Signal_SIGTERM,
			trapSignal:     "TERM",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testPodLogDir := t.TempDir()

			t.Log("Create a pod sandbox")
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "custom-stop-signal-"+tc.name, WithHostNetwork, WithPodLogDirectory(testPodLogDir))

			var (
				testImage     = images.Get(images.BusyBox)
				containerName = "test-container"
			)

			EnsureImageExists(t, testImage)

			t.Log("Create a container with custom stop signal")
			scriptDir := writeStopSignalScript(t)
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("sh", "/stop-signal/stop-signal.sh", tc.trapSignal),
				WithVolumeMount(scriptDir, "/stop-signal"),
				WithLogPath(containerName),
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
			require.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.GetState())
			assert.Equal(t, int32(0), status.GetExitCode(), "container should exit from the configured stop signal trap")

			t.Log("Check container log")
			content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
			require.NoError(t, err)
			checkContainerLog(t, string(content), []string{
				fmt.Sprintf("%s %s Received %s", runtime.Stdout, runtime.LogTagFull, tc.trapSignal),
			})
		})
	}
}

func TestDefaultContainerStopSignal(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipped on Windows.")
	}

	t.Logf("Create a pod config")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "default-stop-signal", WithHostNetwork)

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

func writeStopSignalScript(t *testing.T) string {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "stop-signal.sh")
	script := `#!/bin/sh
set -eu

signal="$1"
child=""

cleanup() {
	echo "Received ${signal}"
	if [ -n "${child}" ]; then
		kill "${child}" 2>/dev/null || true
	fi
	exit 0
}

trap cleanup "${signal}"

sleep 5000 &
child="$!"
wait "${child}"
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0644))
	return scriptDir
}

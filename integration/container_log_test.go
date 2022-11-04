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
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/integration/images"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerLogWithoutTailingNewLine(t *testing.T) {
	testPodLogDir := t.TempDir()

	t.Log("Create a sandbox with log directory")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-log-without-tailing-newline",
		WithPodLogDirectory(testPodLogDir),
	)

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container with log path")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "printf abcd"),
		WithLogPath(containerName),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for container to finish running")
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Log("Check container log")
	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagPartial, "abcd"),
	})
}

func TestLongContainerLog(t *testing.T) {
	testPodLogDir := t.TempDir()

	t.Log("Create a sandbox with log directory")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "long-container-log",
		WithPodLogDirectory(testPodLogDir),
	)

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container with log path")
	config, err := CRIConfig()
	require.NoError(t, err)
	maxSize := config.MaxContainerLogLineSize
	shortLineCmd := fmt.Sprintf("i=0; while [ $i -lt %d ]; do printf %s; i=$((i+1)); done", maxSize-1, "a")
	maxLenLineCmd := fmt.Sprintf("i=0; while [ $i -lt %d ]; do printf %s; i=$((i+1)); done", maxSize, "b")
	longLineCmd := fmt.Sprintf("i=0; while [ $i -lt %d ]; do printf %s; i=$((i+1)); done", maxSize+1, "c")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c",
			fmt.Sprintf("%s; echo; %s; echo; %s; echo", shortLineCmd, maxLenLineCmd, longLineCmd)),
		WithLogPath(containerName),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for container to finish running")
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Log("Check container log")
	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagFull, strings.Repeat("a", maxSize-1)),
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagFull, strings.Repeat("b", maxSize)),
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagPartial, strings.Repeat("c", maxSize)),
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagFull, "c"),
	})
}

func checkContainerLog(t *testing.T, log string, messages []string) {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	require.Len(t, lines, len(messages), "log line number should match")
	for i, line := range lines {
		ts, msg, ok := strings.Cut(line, " ")
		require.True(t, ok)
		_, err := time.Parse(time.RFC3339Nano, ts)
		assert.NoError(t, err, "timestamp should be in RFC3339Nano format")
		assert.Equal(t, messages[i], msg, "log content should match")
	}
}

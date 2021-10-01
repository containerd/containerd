//go:build linux
// +build linux

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestAdditionalGids(t *testing.T) {
	testPodLogDir, err := os.MkdirTemp("/tmp", "additional-gids")
	require.NoError(t, err)
	defer os.RemoveAll(testPodLogDir)

	t.Log("Create a sandbox with log directory")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "additional-gids",
		WithPodLogDirectory(testPodLogDir))

	var (
		testImage     = GetImage(BusyBox)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container to print id")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("id"),
		WithLogPath(containerName),
		WithSupplementalGroups([]int64{1 /*daemon*/, 1234 /*new group*/}),
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

	t.Log("Search additional groups in container log")
	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "groups=1(daemon),10(wheel),1234")
}

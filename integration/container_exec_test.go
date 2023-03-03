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

	"github.com/containerd/containerd/integration/images"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerDrainExecIOAfterExit(t *testing.T) {
	// FIXME(fuweid): support it for windows container.
	if runtime.GOOS == "windows" {
		t.Skip("it seems that windows platform doesn't support detached process. skip it")
	}

	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-exec-drain-io-after-exit")

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container-exec"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 365d"),
	)

	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	t.Log("Exec in container")
	_, _, err = runtimeService.ExecSync(cn, []string{"sh", "-c", "sleep 365d &"}, 5*time.Second)
	require.ErrorContains(t, err, "failed to drain exec process")

	t.Log("Exec in container")
	_, _, err = runtimeService.ExecSync(cn, []string{"sh", "-c", "sleep 2s &"}, 10*time.Second)
	require.NoError(t, err, "should drain IO in time")
}

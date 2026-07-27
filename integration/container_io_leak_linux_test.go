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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerIOLeakAfterStartFailed(t *testing.T) {
	if f := os.Getenv("RUNC_FLAVOR"); f != "" && f != "runc" {
		t.Skip("test requires runc")
	}
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-io-leak-after-start-failed")
	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a container")
	cnConfig := ContainerConfig(
		"containerIOLeakTest",
		testImage,
		WithCommand("something-that-doesnt-exist"),
	)
	t.Log("Create the container")
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	pid := getShimPid(t, sb)
	require.Error(t, runtimeService.StartContainer(cn))
	assert.Equal(t, 0, numPipe(pid))
}

func numPipe(shimPid int) int {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("lsof -p %d | grep pipe", shimPid))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0
	}
	return strings.Count(stdout.String(), "\n")
}

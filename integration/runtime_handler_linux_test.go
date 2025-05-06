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
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TODO(chrisfegly): add/update test(s) to allow testing of multiple runtimes at the same time
func TestRuntimeHandler_Dynamic(t *testing.T) {
	if *runtimeHandler != plugins.RuntimeRuncV2 {
		t.Skipf("Only running test when runtime handler is set to %s", plugins.RuntimeRuncV2)
	}

	runtimeHandler := "test-dynamic-runtime-handler"
	dir := t.TempDir()

	shimPath, err := exec.LookPath("containerd-shim-runc-v2")
	require.NoError(t, err)

	toml := `
runtime_type = "` + plugins.RuntimeRuncV2 + `"
runtime_path = "` + shimPath + `"
`
	err = os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0644)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(defaults.DefaultConfigDir, "runtimes"), 0755)
	require.NoError(t, err)

	handlerPath := filepath.Join(defaults.DefaultConfigDir, "runtimes", runtimeHandler)
	t.Cleanup(func() { os.RemoveAll(handlerPath) })

	err = os.Rename(dir, handlerPath)
	require.NoError(t, err)

	t.Logf("Create a sandbox with runtimeHandler %s", runtimeHandler)

	sbConfig := PodSandboxConfig("sandbox2", "test-runtime-handler")
	sb, err := runtimeService.RunPodSandbox(sbConfig, runtimeHandler)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	})

	t.Logf("Verify runtimeService.PodSandboxStatus() returns previously set runtimeHandler")
	sbStatus, err := runtimeService.PodSandboxStatus(sb)

	require.NoError(t, err)
	assert.Equal(t, runtimeHandler, sbStatus.RuntimeHandler)

	t.Logf("Verify runtimeService.ListPodSandbox() returns previously set runtimeHandler")
	sandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	assert.Equal(t, runtimeHandler, sandboxes[0].RuntimeHandler)
}

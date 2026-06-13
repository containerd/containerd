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
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/integration/images"
)

// TestRuntimeConfigDir verifies that runtime handlers can be loaded from
// individual files in a runtime config directory (runtime_config_dir).
// It starts a private containerd instance whose config.toml points
// runtime_config_dir at a temp directory containing a "my-runc" runtime,
// then runs a pod sandbox using that handler to prove it was loaded.
func TestRuntimeConfigDir(t *testing.T) {
	workDir := t.TempDir()

	// Create runtime config directory with a "my-runc" runtime.
	runtimesDir := filepath.Join(workDir, "runtimes")
	myRuncDir := filepath.Join(runtimesDir, "my-runc")
	require.NoError(t, os.MkdirAll(myRuncDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(myRuncDir, "runtime.toml"),
		[]byte(`runtime_type = "io.containerd.runc.v2"`+"\n"),
		0o600,
	))

	// Write containerd config. The "my-runc" handler is NOT listed here;
	// it should be discovered via runtime_config_dir.
	cfgPath := filepath.Join(workDir, "config.toml")
	cfg := fmt.Sprintf(`
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd]
  default_runtime_name = "runc"
  runtime_config_dir = %q

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
`, runtimesDir)
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	// Start private containerd.
	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	require.NoError(t, ctrd.isReady())

	rSvc := ctrd.criRuntimeService(t)
	iSvc := ctrd.criImageService(t)

	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Dumping containerd config and logs due to test failure")
			dumpFileContent(t, ctrd.configPath())
			dumpFileContent(t, ctrd.logPath())
		}
		cleanupPods(t, rSvc)
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	})

	// Pull an image so we can create a pod.
	testImage := images.Get(images.BusyBox)
	pullImagesByCRI(t, iSvc, testImage)

	// Run a pod sandbox using the "my-runc" handler loaded from the config dir.
	sbCfg := PodSandboxConfig("runtime-config-dir-pod", "runtime-config-dir-test")
	sbID, err := rSvc.RunPodSandbox(sbCfg, "my-runc")
	require.NoError(t, err, "RunPodSandbox with handler 'my-runc' from runtime config dir should succeed")

	// Verify the sandbox reports the correct runtime handler.
	sbStatus, err := rSvc.PodSandboxStatus(sbID)
	require.NoError(t, err)
	assert.Equal(t, "my-runc", sbStatus.RuntimeHandler)
}

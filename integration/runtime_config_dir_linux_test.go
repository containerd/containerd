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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/integration/images"
	podsandboxtypes "github.com/containerd/containerd/v2/internal/cri/server/podsandbox/types"
)

func TestRuntimeConfigDir(t *testing.T) {
	const (
		podAnnotation      = "io.containerd.test.runtime-config-dir"
		podAnnotationValue = "loaded-from-runtime-config-dir"
	)

	workDir := t.TempDir()

	runtimesDir := filepath.Join(workDir, "runtimes")
	myRuncDir := filepath.Join(runtimesDir, "my-runc")
	require.NoError(t, os.MkdirAll(myRuncDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(myRuncDir, "runtime.toml"),
		[]byte(`runtime_type = "io.containerd.runc.v2"
pod_annotations = ["io.containerd.test.runtime-config-dir"]
`),
		0o600,
	))

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

	testImage := images.Get(images.BusyBox)
	pullImagesByCRI(t, iSvc, testImage)

	sbCfg := PodSandboxConfig("runtime-config-dir-pod", "runtime-config-dir-test")
	sbCfg.Annotations[podAnnotation] = podAnnotationValue
	sbID, err := rSvc.RunPodSandbox(sbCfg, "my-runc")
	require.NoError(t, err, "RunPodSandbox with handler 'my-runc' from runtime config dir should succeed")

	sbStatus, err := rSvc.PodSandboxStatus(sbID)
	require.NoError(t, err)
	assert.Equal(t, "my-runc", sbStatus.RuntimeHandler)

	sbStatusResp, err := rSvc.PodSandboxStatusVerbose(sbID)
	require.NoError(t, err)
	var sandboxInfo podsandboxtypes.SandboxInfo
	require.NoError(t, json.Unmarshal([]byte(sbStatusResp.GetInfo()["info"]), &sandboxInfo))
	assert.Equal(t, podAnnotationValue, sandboxInfo.RuntimeSpec.Annotations[podAnnotation])
}

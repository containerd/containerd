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

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestIssue10244LoopbackV2(t *testing.T) {
	testCases := []struct {
		name  string
		value bool
	}{
		{name: "use_internal_loopback=false", value: false},
		{name: "use_internal_loopback=true", value: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, checkLoopbackResult(t, tc.value))
		})
	}
}

// checkLoopbackResult validates whether the loopback interface status within a container is UP
func checkLoopbackResult(t *testing.T, useInternalLoopback bool) bool {
	t.Logf("Create containerd config with 'use_internal_loopback' set to '%t'", useInternalLoopback)
	workDir := t.TempDir()
	configPath := filepath.Join(workDir, "config.toml")
	ctrdConfig := fmt.Sprintf(`
	version = 3

	[plugins]
	[plugins.'io.containerd.cri.v1.runtime']
	  [plugins.'io.containerd.cri.v1.runtime'.cni]
		use_internal_loopback = %t`,
		useInternalLoopback)

	err := os.WriteFile(configPath, []byte(ctrdConfig), 0600)
	require.NoError(t, err)

	t.Logf("Start containerd")
	currentProc := newCtrdProc(t, "containerd", workDir, nil)
	require.NoError(t, currentProc.isReady())

	t.Cleanup(func() {
		t.Log("Cleanup all the pods")
		cleanupPods(t, currentProc.criRuntimeService(t))

		t.Log("Stop containerd process")
		require.NoError(t, currentProc.kill(syscall.SIGTERM))
		require.NoError(t, currentProc.wait(5*time.Minute))
	})

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container-loopback-v2"
	)

	pullImagesByCRI(t, currentProc.criImageService(t), testImage)

	t.Log("Create a pod with a container that checks the loopback status")
	podCtx := newPodTCtx(t, currentProc.criRuntimeService(t), "container-exec-lo-test", "sandbox")
	cnID := podCtx.createContainer(
		containerName,
		testImage,
		runtime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"),
		WithVolumeMount("/usr/local/bin/loopback-v2", "/loopback-v2"),
	)

	t.Logf("Check loopback status - should be UP (not DOWN) when 'use_internal_loopback' is '%t'", useInternalLoopback)
	stdout, _, err := podCtx.rSvc.ExecSync(cnID, []string{"/loopback-v2"}, 5*time.Second)
	require.NoError(t, err, "")
	t.Logf("%s", stdout)

	return assert.Contains(t, string(stdout), "UP")
}

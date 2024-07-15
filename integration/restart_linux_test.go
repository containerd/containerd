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
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func setupUserNSPod(t *testing.T) {
	supportsUserNs := supportsUserNS() && supportsIDMap(defaultRoot) && supportsRuncIDMap() == nil
	if !supportsUserNs {
		t.Log("host or runtime doesn't support userns, skipping pod setup")
		return
	}

	t.Log("adding userns pods for containerd restart test")

	containerID := uint32(0)
	hostID := uint32(65536)
	size := uint32(65536)

	sOpts := []PodSandboxOpts{WithPodUserNs(containerID, hostID, size)}
	sbCfg := PodSandboxConfig("sandbox_ready_userns", "sandbox_ready_userns", sOpts...)
	_, err := runtimeService.RunPodSandbox(sbCfg, *runtimeHandler)
	assert.NoError(t, err)
}

func TestContainerdRestartSandboxRecover(t *testing.T) {
	sbStatuses := map[string]runtime.PodSandboxState{
		// Sandbox with unknown status will be NotReady when returned from ListPodSandbox
		"sandbox_unknown":      runtime.PodSandboxState_SANDBOX_NOTREADY,
		"sandbox_not_ready":    runtime.PodSandboxState_SANDBOX_NOTREADY,
		"sandbox_ready":        runtime.PodSandboxState_SANDBOX_READY,
		"sandbox_ready_userns": runtime.PodSandboxState_SANDBOX_READY,
	}

	setupUserNSPod(t)

	sbReadyConfig := PodSandboxConfig("sandbox_ready", "sandbox_ready")
	_, err := runtimeService.RunPodSandbox(sbReadyConfig, *runtimeHandler)
	assert.NoError(t, err)

	sbNotReadyConfig := PodSandboxConfig("sandbox_not_ready", "sandbox_not_ready")
	notReadyID, err := runtimeService.RunPodSandbox(sbNotReadyConfig, *runtimeHandler)
	assert.NoError(t, err)
	err = runtimeService.StopPodSandbox(notReadyID)
	assert.NoError(t, err)

	t.Logf("Create a pod config with shim create delay")
	sbUnknownConfig := PodSandboxConfig("sandbox_unknown", "sandbox_unknown_status")
	injectShimFailpoint(t, sbUnknownConfig, map[string]string{
		"Create": "1*delay(2000)",
	})
	waitCh := make(chan struct{})
	go func() {
		time.Sleep(time.Second)
		t.Logf("Create a sandbox with shim create delay")
		RestartContainerd(t, syscall.SIGTERM)
		waitCh <- struct{}{}
	}()
	t.Logf("Create a sandbox with shim create delay")
	_, err = runtimeService.RunPodSandbox(sbUnknownConfig, failpointRuntimeHandler)
	assert.Error(t, err)
	<-waitCh
	sbs, err := runtimeService.ListPodSandbox(nil)
	assert.NoError(t, err)
	foundUnkownSb := false
	for _, sb := range sbs {
		if sb.Metadata.Name == "sandbox_unknown" {
			foundUnkownSb = true
		}
		if status, ok := sbStatuses[sb.Metadata.Name]; ok {
			// See: https://github.com/containerd/containerd/issues/10363
			//
			// Test that sandbox IP is still present after restart.
			if sb.State == runtime.PodSandboxState_SANDBOX_READY {
				status, _, err := SandboxInfo(sb.Id)
				require.NoError(t, err)

				assert.NotNil(t, status.Network)
				assert.NotEmpty(t, status.Network.Ip)
			}

			assert.Equal(t, status, sb.State)
			err = runtimeService.StopPodSandbox(sb.Id)
			assert.NoError(t, err)
			err = runtimeService.RemovePodSandbox(sb.Id)
			assert.NoError(t, err)
		}
	}
	assert.True(t, foundUnkownSb)
}

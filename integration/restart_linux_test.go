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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerdRestartSandboxRecover(t *testing.T) {
	sbStatuses := map[string]runtime.PodSandboxState{
		// Sandbox with unknown status will be NotReady when returned from ListPodSandbox
		"sandbox_unknown":   runtime.PodSandboxState_SANDBOX_NOTREADY,
		"sandbox_not_ready": runtime.PodSandboxState_SANDBOX_NOTREADY,
		"sandbox_ready":     runtime.PodSandboxState_SANDBOX_READY,
	}

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
			assert.Equal(t, status, sb.State)
			err = runtimeService.StopPodSandbox(sb.Id)
			assert.NoError(t, err)
			err = runtimeService.RemovePodSandbox(sb.Id)
			assert.NoError(t, err)
		}
	}
	assert.True(t, foundUnkownSb)
}

func TestReload100Pods(t *testing.T) {
	workDir := t.TempDir()

	currentReleaseCtrdDefaultConfig(t, workDir)

	ctrd := newCtrdProc(t, *containerdBin, workDir, []string{})

	logPath := ctrd.logPath()
	t.Cleanup(func() {
		if t.Failed() {
			dumpFileContent(t, logPath)
		}

		t.Log("Ensure there is no leaky state dir after test")
		assert.NoError(t, os.Remove(filepath.Join(workDir, "state", "io.containerd.runtime.v2.task", "k8s.io")))
	})
	require.NoError(t, ctrd.isReady())

	podCtxs := []*podTCtx{}
	defer func() {
		for _, p := range podCtxs {
			p.stop(true)
		}
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	}()

	for i := 0; i < 100; i++ {
		podCtx := newPodTCtx(t,
			ctrd.criRuntimeService(t),
			fmt.Sprintf("test-restart-%d", i),
			"sandbox",
			WithHostNetwork)

		podCtxs = append(podCtxs, podCtx)
	}

	assert.NoError(t, ctrd.kill(syscall.SIGTERM))
	assert.NoError(t, ctrd.wait(5*time.Minute))

	ctrd = newCtrdProc(t, *containerdBin, workDir, []string{})
	require.NoError(t, ctrd.isReady())
}

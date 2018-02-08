/*
Copyright 2017 The Kubernetes Authors.

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

	"github.com/containerd/containerd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
)

// Restart test must run sequentially.
// NOTE(random-liu): Current restart test only support standalone cri-containerd mode.

func TestSandboxAcrossCRIContainerdRestart(t *testing.T) {
	if !*standaloneCRIContainerd {
		t.Skip("Skip because cri-containerd does not run in standalone mode")
	}
	ctx := context.Background()
	sandboxNS := "sandbox-restart-cri-containerd"
	sandboxes := []struct {
		name            string
		id              string
		stateBeforeExit runtime.PodSandboxState
		actionAfterExit string
		expectedState   runtime.PodSandboxState
	}{
		{
			name:            "task-always-ready",
			stateBeforeExit: runtime.PodSandboxState_SANDBOX_READY,
			expectedState:   runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			name:            "task-always-not-ready",
			stateBeforeExit: runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectedState:   runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			name:            "task-exit-before-restart",
			stateBeforeExit: runtime.PodSandboxState_SANDBOX_READY,
			actionAfterExit: "kill",
			expectedState:   runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			name:            "task-deleted-before-restart",
			stateBeforeExit: runtime.PodSandboxState_SANDBOX_READY,
			actionAfterExit: "delete",
			expectedState:   runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
	}
	t.Logf("Make sure no sandbox is running before test")
	existingSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	require.Empty(t, existingSandboxes)

	t.Logf("Start test sandboxes")
	for i := range sandboxes {
		s := &sandboxes[i]
		cfg := PodSandboxConfig(s.name, sandboxNS)
		sb, err := runtimeService.RunPodSandbox(cfg)
		require.NoError(t, err)
		defer func() {
			// Make sure the sandbox is cleaned up in any case.
			runtimeService.StopPodSandbox(sb)
			runtimeService.RemovePodSandbox(sb)
		}()
		s.id = sb
		if s.stateBeforeExit == runtime.PodSandboxState_SANDBOX_NOTREADY {
			require.NoError(t, runtimeService.StopPodSandbox(sb))
		}
	}

	t.Logf("Kill cri-containerd")
	require.NoError(t, KillProcess("cri-containerd"))
	defer func() {
		assert.NoError(t, Eventually(func() (bool, error) {
			return ConnectDaemons() == nil, nil
		}, time.Second, 30*time.Second), "make sure cri-containerd is running before test finish")
	}()

	t.Logf("Change sandbox state, must finish before cri-containerd is restarted")
	for _, s := range sandboxes {
		if s.actionAfterExit == "" {
			continue
		}
		cntr, err := containerdClient.LoadContainer(ctx, s.id)
		require.NoError(t, err)
		task, err := cntr.Task(ctx, nil)
		require.NoError(t, err)
		switch s.actionAfterExit {
		case "kill":
			require.NoError(t, task.Kill(ctx, unix.SIGKILL, containerd.WithKillAll))
		case "delete":
			_, err := task.Delete(ctx, containerd.WithProcessKill)
			require.NoError(t, err)
		}
	}

	t.Logf("Wait until cri-containerd is restarted")
	require.NoError(t, Eventually(func() (bool, error) {
		return ConnectDaemons() == nil, nil
	}, time.Second, 30*time.Second), "wait for cri-containerd to be restarted")

	t.Logf("Check sandbox state after restart")
	loadedSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	assert.Len(t, loadedSandboxes, len(sandboxes))
	for _, s := range sandboxes {
		for _, loaded := range loadedSandboxes {
			if s.id == loaded.Id {
				assert.Equal(t, s.expectedState, loaded.State)
				break
			}
		}
	}

	t.Logf("Should be able to stop and remove sandbox after restart")
	for _, s := range sandboxes {
		// Properly stop the sandbox if it's ready before restart.
		if s.stateBeforeExit == runtime.PodSandboxState_SANDBOX_READY {
			assert.NoError(t, runtimeService.StopPodSandbox(s.id))
		}
		assert.NoError(t, runtimeService.RemovePodSandbox(s.id))
	}
}

// TestSandboxDeletionAcrossCRIContainerdRestart tests the case that sandbox container
// is deleted from containerd during cri-containerd is down. This should not happen.
// However, if this really happens, cri-containerd should not load such sandbox and
// should do best effort cleanup of the sandbox root directory. Note that in this case,
// cri-containerd loses the network namespace of the sandbox, so it won't be able to
// teardown the network properly.
// This test uses host network sandbox to avoid resource leakage.
func TestSandboxDeletionAcrossCRIContainerdRestart(t *testing.T) {
	if !*standaloneCRIContainerd {
		t.Skip("Skip because cri-containerd does not run in standalone mode")
	}
	ctx := context.Background()
	sandboxNS := "sandbox-delete-restart-cri-containerd"
	t.Logf("Make sure no sandbox is running before test")
	existingSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	require.Empty(t, existingSandboxes)

	t.Logf("Start test sandboxes")
	cfg := PodSandboxConfig("sandbox", sandboxNS, WithHostNetwork)
	sb, err := runtimeService.RunPodSandbox(cfg)
	require.NoError(t, err)
	defer func() {
		// Make sure the sandbox is cleaned up in any case.
		runtimeService.StopPodSandbox(sb)
		runtimeService.RemovePodSandbox(sb)
	}()

	t.Logf("Kill cri-containerd")
	require.NoError(t, KillProcess("cri-containerd"))
	defer func() {
		assert.NoError(t, Eventually(func() (bool, error) {
			return ConnectDaemons() == nil, nil
		}, time.Second, 30*time.Second), "make sure cri-containerd is running before test finish")
	}()

	t.Logf("Delete sandbox container from containerd")
	cntr, err := containerdClient.LoadContainer(ctx, sb)
	require.NoError(t, err)
	task, err := cntr.Task(ctx, nil)
	require.NoError(t, err)
	_, err = task.Delete(ctx, containerd.WithProcessKill)
	require.NoError(t, err)
	require.NoError(t, cntr.Delete(ctx, containerd.WithSnapshotCleanup))

	t.Logf("Wait until cri-containerd is restarted")
	require.NoError(t, Eventually(func() (bool, error) {
		return ConnectDaemons() == nil, nil
	}, time.Second, 30*time.Second), "wait for cri-containerd to be restarted")

	t.Logf("Check sandbox state after restart")
	loadedSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	assert.Empty(t, loadedSandboxes)

	t.Logf("Make sure sandbox root is removed")
	sandboxRoot := filepath.Join(*criContainerdRoot, "sandboxes", sb)
	_, err = os.Stat(sandboxRoot)
	assert.True(t, os.IsNotExist(err))
}

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
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	apitask "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestIssue7496_ShouldRetryShutdown is based on https://github.com/containerd/containerd/issues/7496.
//
// Assume that the shim.Delete takes almost 10 seconds and returns successfully
// and there is no container in shim. However, the context is close to be
// canceled. It will fail to call Shutdown. If we ignores the Canceled error,
// the shim will be leaked. In order to reproduce this, this case will use
// failpoint to inject error into Shutdown API, and then check whether the shim
// is leaked.
func TestIssue7496_ShouldRetryShutdown(t *testing.T) {
	// TODO: re-enable if we can retry Shutdown API.
	t.Skipf("Please re-enable me if we can retry Shutdown API")

	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	t.Logf("Create a pod config with shutdown failpoint")
	sbConfig := PodSandboxConfig("sandbox", "issue7496_shouldretryshutdown")
	injectShimFailpoint(t, sbConfig, map[string]string{
		"Shutdown": "1*error(please retry)",
	})

	t.Logf("RunPodSandbox")
	sbID, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.NoError(t, err)

	t.Logf("Connect to the shim %s", sbID)
	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)

	t.Logf("Log shim %s's pid: %d", sbID, shimPid(ctx, t, shimCli))

	t.Logf("StopPodSandbox and RemovePodSandbox")
	require.NoError(t, runtimeService.StopPodSandbox(sbID))
	require.NoError(t, runtimeService.RemovePodSandbox(sbID))

	t.Logf("Check the shim connection")
	_, err = shimCli.Connect(ctx, &apitask.ConnectRequest{})
	require.Error(t, err, "should failed to call shim connect API")
}

func TestShutdownShimWhenPauseExitsBeforeWorkload(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")

	t.Logf("RunPodSandbox")
	sbConfig := PodSandboxConfig("sandbox", t.Name(), WithHostNetwork)
	sbID, err := runtimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = runtimeService.StopPodSandbox(sbID)
		_ = runtimeService.RemovePodSandbox(sbID)
	})

	t.Logf("Connect to the shim %s", sbID)
	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a container - sleep 1d")
	containerName := "test-container"
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 1d"),
		WithPidNamespace(runtime.NamespaceMode_CONTAINER),
	)
	cnID, err := runtimeService.CreateContainer(sbID, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cnID))

	t.Log("Load pause task and wait")
	pauseContainer, err := containerdClient.LoadContainer(ctx, sbID)
	require.NoError(t, err)
	pauseTask, err := pauseContainer.Task(ctx, nil)
	require.NoError(t, err)
	pauseExitCh, err := pauseTask.Wait(ctx)
	require.NoError(t, err)

	t.Log("Load workload task and wait")
	workloadContainer, err := containerdClient.LoadContainer(ctx, cnID)
	require.NoError(t, err)
	workloadTask, err := workloadContainer.Task(ctx, nil)
	require.NoError(t, err)
	workloadExitCh, err := workloadTask.Wait(ctx)
	require.NoError(t, err)

	t.Log("Kill pause container by containerd client API")
	require.NoError(t, pauseTask.Kill(ctx, syscall.SIGKILL))

	select {
	case status := <-pauseExitCh:
		pauseExitStatus, _, err := status.Result()
		require.NoError(t, err)
		require.Equal(t, uint32(137), pauseExitStatus)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for pause task exit")
	}

	t.Log("Wait for pause task deletion")
	require.NoError(t, Eventually(func() (bool, error) {
		_, err := pauseContainer.Task(ctx, nil)
		if err == nil {
			return false, nil
		}

		if errdefs.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, time.Second, 30*time.Second))

	t.Log("Stop sandbox and wait for workload")
	require.NoError(t, runtimeService.StopPodSandbox(sbID))

	select {
	case status := <-workloadExitCh:
		workloadExitStatus, _, err := status.Result()
		require.NoError(t, err)
		require.Equal(t, uint32(137), workloadExitStatus)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for workload task exit")
	}

	t.Log("Remove sandbox")
	require.NoError(t, runtimeService.RemovePodSandbox(sbID))

	t.Log("Shim should be shutdown")
	_, err = shimCli.Connect(ctx, &apitask.ConnectRequest{})
	require.Error(t, err)
	require.ErrorContains(t, err, "ttrpc: closed")
}

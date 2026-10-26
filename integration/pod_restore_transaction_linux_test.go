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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// These tests restart the integration daemon and must run sequentially on a
// dedicated test host, like the other containerd restart tests.
func TestPodRestoreTransactionRestart(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("the pause-container checkpoint backend requires runc")
	}
	requirePodCheckpointCRIU(t)
	sourceID, sourceConfig, appID, appConfig := runPodCheckpointCounter(t, t.Name()+"-source")
	sidecarConfig := proto.Clone(appConfig).(*runtime.ContainerConfig)
	sidecarConfig.Metadata.Name = "sidecar"
	sidecarID, err := runtimeService.CreateContainer(sourceID, sidecarConfig, sourceConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(sidecarID))
	before := map[string]podCheckpointCounter{
		appConfig.GetMetadata().GetName(): waitForPodCheckpointCounter(t, appID, "", 5),
		"sidecar":                         waitForPodCheckpointCounter(t, sidecarID, "", 5),
	}
	checkpointDir := t.TempDir()
	require.NoError(t, runtimeService.CheckpointPod(&runtime.CheckpointPodRequest{
		PodSandboxId: sourceID, ContainerIds: []string{appID, sidecarID}, OutputPath: checkpointDir,
	}))
	for name, id := range map[string]string{appConfig.GetMetadata().GetName(): appID, "sidecar": sidecarID} {
		current := readPodCheckpointCounter(t, id)
		waitForPodCheckpointCounter(t, id, before[name].token, current.count+1)
	}

	request := &runtime.RestorePodRequest{
		CheckpointPath:   checkpointDir,
		Config:           podCheckpointSandboxConfig(t, t.Name()+"-restored"),
		ContainerConfigs: []*runtime.ContainerConfig{appConfig, sidecarConfig},
		RuntimeHandler:   *runtimeHandler,
	}
	response, err := runtimeService.RestorePod(request)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(response.GetPodSandboxId()))
		assert.NoError(t, runtimeService.RemovePodSandbox(response.GetPodSandboxId()))
	})
	require.Len(t, response.GetRestoredContainers(), 2)
	for _, restored := range response.GetRestoredContainers() {
		state, err := runtimeService.ContainerStatus(restored.GetContainerId())
		require.NoError(t, err)
		assert.Equal(t, runtime.ContainerState_CONTAINER_CREATED, state.GetState())
	}

	// A committed restore survives a daemon restart: the containers stay
	// CREATED and still restore their processes when started.
	RestartContainerd(t, syscall.SIGKILL)

	for i, restored := range response.GetRestoredContainers() {
		state, err := runtimeService.ContainerStatus(restored.GetContainerId())
		require.NoError(t, err)
		assert.Equal(t, runtime.ContainerState_CONTAINER_CREATED, state.GetState())
		require.NoError(t, runtimeService.StartContainer(restored.GetContainerId()))
		first := readPodCheckpointCounter(t, restored.GetContainerId())
		previous := before[restored.GetName()]
		resumed := waitForPodCheckpointCounter(t, restored.GetContainerId(), previous.token, first.count+1)
		assert.Greater(t, resumed.count, previous.count)
		if i == 0 {
			other, err := runtimeService.ContainerStatus(response.GetRestoredContainers()[1].GetContainerId())
			require.NoError(t, err)
			assert.Equal(t, runtime.ContainerState_CONTAINER_CREATED, other.GetState(), "starting one restored container must not start the other")
		}
	}
}

func TestPodRestoreInterruptedSandboxCreation(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("the pause-container checkpoint backend requires runc")
	}
	requirePodCheckpointCRIU(t)
	sourceID, _, containerID, containerConfig := runPodCheckpointCounter(t, t.Name()+"-source")
	checkpointDir := t.TempDir()
	require.NoError(t, runtimeService.CheckpointPod(&runtime.CheckpointPodRequest{
		PodSandboxId: sourceID, ContainerIds: []string{containerID}, OutputPath: checkpointDir,
	}))
	config := podCheckpointSandboxConfig(t, t.Name()+"-restored")
	labels := map[string]string{"pod-restore-transaction": t.Name()}
	config.Labels = labels
	injectCNIFailpoint(t, config, &failpointConf{Add: "1*delay(86400000)"})
	request := &runtime.RestorePodRequest{
		CheckpointPath: checkpointDir, Config: config,
		ContainerConfigs: []*runtime.ContainerConfig{containerConfig}, RuntimeHandler: failpointRuntimeHandler,
	}
	result := make(chan error, 1)
	go func() {
		_, err := runtimeService.RestorePod(request)
		result <- err
	}()
	require.NoError(t, ensureCNIAddRunning(t, config.GetMetadata().GetName()))
	RestartContainerd(t, syscall.SIGKILL)
	require.Error(t, <-result)
	remaining, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{LabelSelector: labels})
	require.NoError(t, err)
	assert.Empty(t, remaining, "startup recovery must remove the interrupted restore sandbox")
}

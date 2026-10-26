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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// The counter process keeps a random token and a count in shell memory and
// writes both to /counter. A restored process continues with the same token
// and a larger count; a freshly started process writes a new token.
const podCheckpointCounterScript = `token=$(cat /proc/sys/kernel/random/uuid)
i=0
while true; do
	i=$((i+1))
	echo "$token $i" > /counter
	sleep 0.2
done`

func TestPodCheckpointRestore(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("the pause-container backend supports only runc; see TestPodCheckpointUnsupportedRuntime")
	}
	requirePodCheckpointCRIU(t)

	t.Log("Run a Pod with a container that counts in memory")
	sourceID, _, containerID, containerConfig := runPodCheckpointCounter(t, "source")
	before := waitForPodCheckpointCounter(t, containerID, "", 5)

	t.Log("Checkpoint the Pod")
	checkpointDir := t.TempDir()
	require.NoError(t, runtimeService.CheckpointPod(&runtime.CheckpointPodRequest{
		PodSandboxId: sourceID,
		ContainerIds: []string{containerID},
		OutputPath:   checkpointDir,
	}))
	assert.FileExists(t, filepath.Join(checkpointDir, "checkpoint-manifest.json"))

	t.Log("Check that the source container was resumed")
	afterCheckpoint := readPodCheckpointCounter(t, containerID)
	waitForPodCheckpointCounter(t, containerID, before.token, afterCheckpoint.count+1)
	status, err := runtimeService.PodSandboxStatus(sourceID)
	require.NoError(t, err)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_READY, status.GetState())

	t.Log("Restore the checkpoint into a new Pod")
	restoreConfig := podCheckpointSandboxConfig(t, "restored")
	response, err := runtimeService.RestorePod(&runtime.RestorePodRequest{
		CheckpointPath:   checkpointDir,
		Config:           restoreConfig,
		ContainerConfigs: []*runtime.ContainerConfig{containerConfig},
		RuntimeHandler:   *runtimeHandler,
	})
	require.NoError(t, err)
	restoredID := response.GetPodSandboxId()
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(restoredID))
		assert.NoError(t, runtimeService.RemovePodSandbox(restoredID))
	})
	assert.NotEqual(t, sourceID, restoredID)
	require.Len(t, response.GetRestoredContainers(), 1)
	restored := response.GetRestoredContainers()[0]
	assert.Equal(t, containerConfig.GetMetadata().GetName(), restored.GetName())
	assert.NotEqual(t, containerID, restored.GetContainerId())

	t.Log("Check that the restored container waits for StartContainer")
	restoredStatus, err := runtimeService.ContainerStatus(restored.GetContainerId())
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_CREATED, restoredStatus.GetState())

	t.Log("Start the restored container and check that its process continues")
	require.NoError(t, runtimeService.StartContainer(restored.GetContainerId()))
	// /counter is restored with the writable layer, so wait for a write made
	// by the restored process. A new process would write a different token.
	firstRead := readPodCheckpointCounter(t, restored.GetContainerId())
	resumed := waitForPodCheckpointCounter(t, restored.GetContainerId(), before.token, firstRead.count+1)
	assert.Greater(t, resumed.count, before.count)
}

// TestPodCheckpointUnsupportedRuntime checks that the pause-container backend
// rejects a runtime other than the runc v2 shim before it freezes the Pod.
// Freezing a gVisor Pod would also stop the sandbox that the runsc shim needs
// to report task state.
func TestPodCheckpointUnsupportedRuntime(t *testing.T) {
	if !runtimeHandlerIsRunsc() {
		t.Skip("test requires the runsc runtime handler")
	}

	sandboxID, _, containerID, _ := runPodCheckpointCounter(t, "source")
	before := waitForPodCheckpointCounter(t, containerID, "", 5)

	checkpointDir := t.TempDir()
	request := &runtime.CheckpointPodRequest{
		PodSandboxId: sandboxID,
		ContainerIds: []string{containerID},
		OutputPath:   checkpointDir,
	}
	err := runtimeService.CheckpointPod(request)
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err), "unexpected error: %v", err)

	entries, err := os.ReadDir(checkpointDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a failed checkpoint must not leave partial output")

	t.Log("Check that the container keeps running")
	containerStatus, err := runtimeService.ContainerStatus(containerID)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, containerStatus.GetState())
	afterCheckpoint := readPodCheckpointCounter(t, containerID)
	waitForPodCheckpointCounter(t, containerID, before.token, afterCheckpoint.count+1)

	t.Log("Check that a rejected checkpoint leaves no state behind")
	err = runtimeService.CheckpointPod(request)
	assert.Equal(t, codes.Unimplemented, status.Code(err), "unexpected error: %v", err)
}

func requirePodCheckpointCRIU(t *testing.T) {
	t.Helper()
	if flavor := os.Getenv("RUNC_FLAVOR"); flavor != "" && flavor != "runc" {
		t.Skip("test requires runc")
	}
	if _, err := exec.LookPath("criu"); err != nil {
		t.Skip("the pause-container checkpoint backend requires criu")
	}
}

// podCheckpointSandboxConfig returns a sandbox config with a dedicated Pod
// cgroup parent, which the pause-container backend freezes during capture.
// PodSandboxConfig shares one parent between all Pods.
func podCheckpointSandboxConfig(t *testing.T, name string) *runtime.PodSandboxConfig {
	t.Helper()
	config := PodSandboxConfig(name, "pod-checkpoint")
	id := util.GenerateID()[:12]

	// PodSandboxConfig sets a parent only for the systemd cgroup driver.
	if config.GetLinux().GetCgroupParent() != "" {
		slice := "containerd-test-podcheckpoint" + id + ".slice"
		config.Linux.CgroupParent = "/" + slice
		// Registered before the Pod cleanup, so this runs after the Pod is removed.
		t.Cleanup(func() {
			if output, err := exec.Command("systemctl", "stop", slice).CombinedOutput(); err != nil {
				t.Logf("failed to stop Pod slice %q: %v: %s", slice, err, output)
			}
		})
		return config
	}

	cgroupParent := "/containerd-test-podcheckpoint" + id
	config.Linux.CgroupParent = cgroupParent
	t.Cleanup(func() {
		paths, _ := filepath.Glob(filepath.Join("/sys/fs/cgroup", "*", cgroupParent))
		paths = append(paths, filepath.Join("/sys/fs/cgroup", cgroupParent))
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Logf("failed to remove Pod cgroup %q: %v", path, err)
			}
		}
	})
	return config
}

func runPodCheckpointCounter(t *testing.T, name string) (string, *runtime.PodSandboxConfig, string, *runtime.ContainerConfig) {
	t.Helper()
	sandboxConfig := podCheckpointSandboxConfig(t, name)
	sandboxID, err := runtimeService.RunPodSandbox(sandboxConfig, *runtimeHandler)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sandboxID))
		assert.NoError(t, runtimeService.RemovePodSandbox(sandboxID))
	})

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)
	containerConfig := ContainerConfig("counter", testImage,
		WithCommand("sh", "-c", podCheckpointCounterScript),
	)
	containerID, err := runtimeService.CreateContainer(sandboxID, containerConfig, sandboxConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(containerID))
	return sandboxID, sandboxConfig, containerID, containerConfig
}

type podCheckpointCounter struct {
	token string
	count int
}

func readPodCheckpointCounter(t *testing.T, containerID string) podCheckpointCounter {
	t.Helper()
	return waitForPodCheckpointCounter(t, containerID, "", 0)
}

func execPodCheckpointCounter(containerID string) (podCheckpointCounter, error) {
	stdout, stderr, err := runtimeService.ExecSync(containerID, []string{"cat", "/counter"}, 10*time.Second)
	if err != nil {
		return podCheckpointCounter{}, err
	}
	fields := strings.Fields(string(stdout))
	if len(fields) != 2 {
		return podCheckpointCounter{}, errors.New("unexpected counter output: " + string(stdout) + string(stderr))
	}
	count, err := strconv.Atoi(fields[1])
	if err != nil {
		return podCheckpointCounter{}, err
	}
	return podCheckpointCounter{token: fields[0], count: count}, nil
}

// waitForPodCheckpointCounter waits until the counter reaches minCount. A
// non-empty token must match the counter's token while waiting.
func waitForPodCheckpointCounter(t *testing.T, containerID, token string, minCount int) podCheckpointCounter {
	t.Helper()
	var counter podCheckpointCounter
	require.NoError(t, Eventually(func() (bool, error) {
		var err error
		counter, err = execPodCheckpointCounter(containerID)
		if err != nil {
			// The file may be written while it is read.
			return false, nil
		}
		if token != "" && counter.token != token {
			return false, errors.New("counter was restarted by a new process")
		}
		return counter.count >= minCount, nil
	}, 200*time.Millisecond, 30*time.Second))
	return counter
}

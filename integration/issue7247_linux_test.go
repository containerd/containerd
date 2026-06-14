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

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// As described in issue 7247, when the disk is full, restarting containerd will result in inconsistent data between cri and metadata.
// This e2e is ensuring data consistency even if the disk is full.
func TestIssue7247(t *testing.T) {
	var (
		busyboxImage = images.Get(images.BusyBox)
		pauseImage   = images.Get(images.Pause)
		err          error
	)
	testutil.RequiresRoot(t)

	workDir, err := prepareWorkDir(t, 1<<30) //1GB
	require.NoError(t, err)

	newContainerdProc := newCtrdProc(t, "containerd", workDir.Dir(), nil)
	require.NoError(t, newContainerdProc.isReady())

	t.Cleanup(func() {
		t.Log("Cleanup all the pods")
		cleanupPods(t, newContainerdProc.criRuntimeService(t))

		t.Log("Stopping current release's containerd process")
		require.NoError(t, newContainerdProc.kill(syscall.SIGTERM))
		require.NoError(t, newContainerdProc.wait(5*time.Minute))

		t.Log("Unmount workdir and remove")
		workDir.Close()
	})

	_, err = newContainerdProc.criImageService(t).PullImage(&runtime.ImageSpec{Image: busyboxImage}, nil, nil, "")
	require.NoError(t, err)
	_, err = newContainerdProc.criImageService(t).PullImage(&runtime.ImageSpec{Image: pauseImage}, nil, nil, "")
	require.NoError(t, err)

	// Create a sandbox
	sbConfig := PodSandboxConfig("sandbox", "sandbox")
	sbID, err := newContainerdProc.criRuntimeService(t).RunPodSandbox(sbConfig, *runtimeHandler)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sbID))
		assert.NoError(t, runtimeService.RemovePodSandbox(sbID))
	}()
	stateIDs := map[runtime.ContainerState]string{}
	for _, cnt := range []struct {
		name        string
		commands    []string
		execs       [][]string
		expectState runtime.ContainerState
	}{
		{
			name:        "created",
			expectState: runtime.ContainerState_CONTAINER_CREATED,
		},
		{
			name:        "exited",
			commands:    []string{"-c", "echo", "exited"},
			expectState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			name:     "running",
			commands: []string{"-c", "sleep", "1d"},
			execs: [][]string{
				{"dd", "if=/dev/zero", "of=/1g", "bs=1M", "count=1024"},
				{"dd", "if=/dev/zero", "of=/1m", "bs=1k", "count=1024"},
			},
			expectState: runtime.ContainerState_CONTAINER_RUNNING,
		},
	} {
		containerConfig := ContainerConfig(cnt.name, busyboxImage,
			WithCommand("/bin/sh", cnt.commands...),
		)
		// Create the container
		containerID, err := newContainerdProc.criRuntimeService(t).CreateContainer(sbID, containerConfig, sbConfig)
		assert.NoError(t, err)
		if cnt.expectState != runtime.ContainerState_CONTAINER_CREATED {
			// Start the container
			err = newContainerdProc.criRuntimeService(t).StartContainer(containerID)
			assert.NoError(t, err)
			if len(cnt.execs) > 0 {
				for _, exec := range cnt.execs {
					newContainerdProc.criRuntimeService(t).ExecSync(containerID, exec, 60*time.Second)
				}
			}
		}
		stateIDs[cnt.expectState] = containerID
	}
	defer func() {
		for state, id := range stateIDs {
			if state != runtime.ContainerState_CONTAINER_CREATED {
				// Stop the container
				err = newContainerdProc.criRuntimeService(t).StopContainer(id, 10)
				assert.NoError(t, err)
			}
			// Remove the container
			err = newContainerdProc.criRuntimeService(t).RemoveContainer(id)
			assert.NoError(t, err)
		}
	}()
	// Restart containerd
	err = newContainerdProc.restart(t)
	assert.NoError(t, err)

	// Check if the container state is correct
	containers, err := newContainerdProc.criRuntimeService(t).ListContainers(&runtime.ContainerFilter{})
	assert.NoError(t, err)
	assert.Equal(t, len(stateIDs), len(containers))
	for _, cnt := range containers {
		assert.Equal(t, stateIDs[cnt.State], cnt.Id)
	}
}

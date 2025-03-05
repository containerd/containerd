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

	workDir, err := prepareWorkDir(t)
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

	// Create a container config
	containerConfig := ContainerConfig("nospace", busyboxImage,
		WithCommand("/bin/sh", "-c",
			"dd if=/dev/zero of=/data1 bs=2M count=1024 && dd if=/dev/zero of=/data2 bs=1k count=4096", // 2G + 4M
		),
	)

	// Create the container
	containerID, err := newContainerdProc.criRuntimeService(t).CreateContainer(sbID, containerConfig, sbConfig)
	assert.NoError(t, err)
	filter := &runtime.ContainerFilter{
		Id: containerID,
	}

	// Start the container
	err = newContainerdProc.criRuntimeService(t).StartContainer(containerID)
	assert.NoError(t, err)

	// Wait for the container write success
	for {
		exited := false
		time.Sleep(10 * time.Second) // Give it time to write the file
		containers, err := newContainerdProc.criRuntimeService(t).ListContainers(filter)
		assert.NoError(t, err)
		for _, container := range containers {
			if container.Id == containerID &&
				container.State == runtime.ContainerState_CONTAINER_EXITED {
				exited = true
			}
		}
		if exited {
			break
		}
	}

	// Restart containerd
	err = newContainerdProc.restart(t)
	assert.NoError(t, err)

	// Check if the container is still visible
	containers, err := newContainerdProc.criRuntimeService(t).ListContainers(filter)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(containers))
	assert.Equal(t, containerID, containers[0].Id)
}

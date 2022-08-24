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
	goruntime "runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Restart test must run sequentially.

func TestContainerdRestart(t *testing.T) {
	type container struct {
		name  string
		id    string
		state runtime.ContainerState
	}
	type sandbox struct {
		name       string
		id         string
		state      runtime.PodSandboxState
		containers []container
	}
	ctx := context.Background()
	sandboxNS := "restart-containerd"
	sandboxes := []sandbox{
		{
			name:  "ready-sandbox",
			state: runtime.PodSandboxState_SANDBOX_READY,
			containers: []container{
				{
					name:  "created-container",
					state: runtime.ContainerState_CONTAINER_CREATED,
				},
				{
					name:  "running-container",
					state: runtime.ContainerState_CONTAINER_RUNNING,
				},
				{
					name:  "exited-container",
					state: runtime.ContainerState_CONTAINER_EXITED,
				},
			},
		},
		{
			name:  "notready-sandbox",
			state: runtime.PodSandboxState_SANDBOX_NOTREADY,
			containers: []container{
				{
					name:  "created-container",
					state: runtime.ContainerState_CONTAINER_CREATED,
				},
				{
					name:  "exited-container",
					state: runtime.ContainerState_CONTAINER_EXITED,
				},
			},
		},
	}
	// NOTE(claudiub): The test will set the container's Linux.SecurityContext.NamespaceOptions.Pid = NamespaceMode_CONTAINER,
	// and the expectation is that the container will keep running even if the sandbox container dies.
	// We do not have that option on Windows.
	if goruntime.GOOS != "windows" {
		sandboxes[1].containers = append(sandboxes[1].containers, container{
			name:  "running-container",
			state: runtime.ContainerState_CONTAINER_RUNNING,
		})
	}

	t.Logf("Make sure no sandbox is running before test")
	existingSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	require.Empty(t, existingSandboxes)

	t.Logf("Start test sandboxes and containers")
	for i := range sandboxes {
		s := &sandboxes[i]
		sbCfg := PodSandboxConfig(s.name, sandboxNS)
		sid, err := runtimeService.RunPodSandbox(sbCfg, *runtimeHandler)
		require.NoError(t, err)
		defer func() {
			// Make sure the sandbox is cleaned up in any case.
			runtimeService.StopPodSandbox(sid)
			runtimeService.RemovePodSandbox(sid)
		}()

		pauseImage := images.Get(images.Pause)
		EnsureImageExists(t, pauseImage)

		s.id = sid
		for j := range s.containers {
			c := &s.containers[j]
			cfg := ContainerConfig(c.name, pauseImage,
				// Set pid namespace as per container, so that container won't die
				// when sandbox container is killed.
				WithPidNamespace(runtime.NamespaceMode_CONTAINER),
			)
			cid, err := runtimeService.CreateContainer(sid, cfg, sbCfg)
			require.NoError(t, err)
			// Reply on sandbox cleanup.
			c.id = cid
			switch c.state {
			case runtime.ContainerState_CONTAINER_CREATED:
			case runtime.ContainerState_CONTAINER_RUNNING:
				require.NoError(t, runtimeService.StartContainer(cid))
			case runtime.ContainerState_CONTAINER_EXITED:
				require.NoError(t, runtimeService.StartContainer(cid))
				require.NoError(t, runtimeService.StopContainer(cid, 10))
			}
		}
		if s.state == runtime.PodSandboxState_SANDBOX_NOTREADY {
			cntr, err := containerdClient.LoadContainer(ctx, sid)
			require.NoError(t, err)
			task, err := cntr.Task(ctx, nil)
			require.NoError(t, err)

			waitCh, err := task.Wait(ctx)
			require.NoError(t, err)

			err = task.Kill(ctx, syscall.SIGKILL, containerd.WithKillAll)
			// NOTE: CRI-plugin setups watcher for each container and
			// cleanups container when the watcher returns exit event.
			// We just need to kill that sandbox and wait for exit
			// event from waitCh. If the sandbox container exits,
			// the state of sandbox must be NOT_READY.
			require.NoError(t, err)

			select {
			case <-waitCh:
			case <-time.After(30 * time.Second):
				t.Fatalf("expected to receive exit event in time, but timeout")
			}
		}
	}

	t.Logf("Pull test images")
	for _, image := range []string{images.Get(images.BusyBox), images.Get(images.Pause)} {
		EnsureImageExists(t, image)
	}
	imagesBeforeRestart, err := imageService.ListImages(nil)
	assert.NoError(t, err)

	t.Logf("Restart containerd")
	RestartContainerd(t)

	t.Logf("Check sandbox and container state after restart")
	loadedSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	assert.Len(t, loadedSandboxes, len(sandboxes))
	loadedContainers, err := runtimeService.ListContainers(&runtime.ContainerFilter{})
	require.NoError(t, err)
	assert.Len(t, loadedContainers, len(sandboxes[0].containers)+len(sandboxes[1].containers))
	for _, s := range sandboxes {
		for _, loaded := range loadedSandboxes {
			if s.id == loaded.Id {
				t.Logf("Checking sandbox state for '%s'", s.name)
				assert.Equal(t, s.state, loaded.State)
				break
			}
		}
		for _, c := range s.containers {
			for _, loaded := range loadedContainers {
				if c.id == loaded.Id {
					t.Logf("Checking container state for '%s' in sandbox '%s'", c.name, s.name)
					assert.Equal(t, c.state, loaded.State)
					break
				}
			}
		}
	}

	t.Logf("Should be able to stop and remove sandbox after restart")
	for _, s := range sandboxes {
		assert.NoError(t, runtimeService.StopPodSandbox(s.id))
		assert.NoError(t, runtimeService.RemovePodSandbox(s.id))
	}

	t.Logf("Should recover all images")
	imagesAfterRestart, err := imageService.ListImages(nil)
	assert.NoError(t, err)
	assert.Equal(t, len(imagesBeforeRestart), len(imagesAfterRestart))
	for _, i1 := range imagesBeforeRestart {
		found := false
		for _, i2 := range imagesAfterRestart {
			if i1.Id == i2.Id {
				sort.Strings(i1.RepoTags)
				sort.Strings(i1.RepoDigests)
				sort.Strings(i2.RepoTags)
				sort.Strings(i2.RepoDigests)
				assert.Equal(t, i1, i2)
				found = true
				break
			}
		}
		assert.True(t, found, "should find image %+v", i1)
	}
}

// TODO: Add back the unknown state test.

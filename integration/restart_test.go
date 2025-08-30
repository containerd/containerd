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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/integration/images"
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
				{name: "created-container", state: runtime.ContainerState_CONTAINER_CREATED},
				{name: "running-container", state: runtime.ContainerState_CONTAINER_RUNNING},
				{name: "exited-container", state: runtime.ContainerState_CONTAINER_EXITED},
			},
		},
		{
			name:  "notready-sandbox",
			state: runtime.PodSandboxState_SANDBOX_NOTREADY,
			containers: []container{
				{name: "created-container", state: runtime.ContainerState_CONTAINER_CREATED},
				{name: "exited-container", state: runtime.ContainerState_CONTAINER_EXITED},
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

	waitOutsideClear := 45 * time.Second
	if goruntime.GOOS == "windows" {
		waitOutsideClear = 90 * time.Second
	}
	if !waitNoExternalSandboxes(t, sandboxNS, waitOutsideClear) {
		t.Skipf("skip restart test: other sandboxes are running outside ns %q", sandboxNS)
	}

	existingSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	var staleInNS []string
	for _, ps := range existingSandboxes {
		if ps.GetMetadata() != nil && ps.GetMetadata().Namespace == sandboxNS {
			staleInNS = append(staleInNS, ps.Id)
		}
	}
	if len(staleInNS) > 0 {
		for _, id := range staleInNS {
			_ = runtimeService.StopPodSandbox(id)
			_ = runtimeService.RemovePodSandbox(id)
		}
		existingSandboxes, err = runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
		require.NoError(t, err)
		for _, ps := range existingSandboxes {
			if ps.GetMetadata() != nil && ps.GetMetadata().Namespace == sandboxNS {
				t.Fatalf("expected no sandboxes in ns %q, still have %q", sandboxNS, ps.Id)
			}
		}
	}

	for i := range sandboxes {
		s := &sandboxes[i]
		sbCfg := PodSandboxConfig(s.name, sandboxNS)
		sid, err := runtimeService.RunPodSandbox(sbCfg, *runtimeHandler)
		require.NoError(t, err)
		defer func(id string) {
			// Make sure the sandbox is cleaned up in any case.
			_ = runtimeService.StopPodSandbox(id)
			_ = runtimeService.RemovePodSandbox(id)
		}(sid)

		pauseImage := images.Get(images.Pause)
		EnsureImageExists(t, pauseImage)

		s.id = sid
		for j := range s.containers {
			c := &s.containers[j]
			cfg := ContainerConfig(c.name, pauseImage, WithPidNamespace(runtime.NamespaceMode_CONTAINER))
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

	if !waitNoExternalSandboxes(t, sandboxNS, waitOutsideClear) {
		t.Skipf("skip restart test before restart: other sandboxes are running outside ns %q", sandboxNS)
	}

	t.Logf("Restart containerd")
	RestartContainerd(t, syscall.SIGTERM)

	t.Logf("Check sandbox and container state after restart")
	loadedSandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	var ourSandboxes []*runtime.PodSandbox
	for _, ps := range loadedSandboxes {
		if ps.GetMetadata() != nil && ps.GetMetadata().Namespace == sandboxNS {
			ourSandboxes = append(ourSandboxes, ps)
		}
	}

	expectedSBCount := len(sandboxes)
	sbDeadline := 15 * time.Second
	if goruntime.GOOS == "windows" {
		sbDeadline = 40 * time.Second
	}
	sbEnd := time.Now().Add(sbDeadline)
	for len(ourSandboxes) != expectedSBCount && time.Now().Before(sbEnd) {
		time.Sleep(200 * time.Millisecond)
		ourSandboxes = ourSandboxes[:0]
		loadedSandboxes, err = runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
		require.NoError(t, err)
		for _, ps := range loadedSandboxes {
			if ps.GetMetadata() != nil && ps.GetMetadata().Namespace == sandboxNS {
				ourSandboxes = append(ourSandboxes, ps)
			}
		}
	}
	assert.Len(t, ourSandboxes, expectedSBCount)

	ourSBIDs := make(map[string]struct{}, len(sandboxes))
	for _, s := range sandboxes {
		ourSBIDs[s.id] = struct{}{}
	}

	loadedContainers, err := runtimeService.ListContainers(&runtime.ContainerFilter{})
	require.NoError(t, err)
	var ourContainers []*runtime.Container
	for _, c := range loadedContainers {
		if _, ok := ourSBIDs[c.PodSandboxId]; ok {
			ourContainers = append(ourContainers, c)
		}
	}
	expectedContainers := 0
	for _, s := range sandboxes {
		expectedContainers += len(s.containers)
	}

	ctDeadline := 15 * time.Second
	if goruntime.GOOS == "windows" {
		ctDeadline = 40 * time.Second
	}
	ctEnd := time.Now().Add(ctDeadline)
	for len(ourContainers) != expectedContainers && time.Now().Before(ctEnd) {
		time.Sleep(200 * time.Millisecond)
		ourContainers = ourContainers[:0]
		loadedContainers, err = runtimeService.ListContainers(&runtime.ContainerFilter{})
		require.NoError(t, err)
		for _, c := range loadedContainers {
			if _, ok := ourSBIDs[c.PodSandboxId]; ok {
				ourContainers = append(ourContainers, c)
			}
		}
	}
	assert.Len(t, ourContainers, expectedContainers)

	for _, s := range sandboxes {
		for _, loaded := range ourSandboxes {
			if s.id == loaded.Id {
				t.Logf("Checking sandbox state for '%s'", s.name)
				assert.Equal(t, s.state, loaded.State)

				// See https://github.com/containerd/containerd/issues/7843 for details.
				// Test that CNI result and sandbox IPs are still present after restart.
				if loaded.State == runtime.PodSandboxState_SANDBOX_READY {
					status, info, err := SandboxInfo(loaded.Id)
					require.NoError(t, err)

					// Check that the NetNS didn't close on us, that we still have
					// the CNI result, and that we still have the IP we were given
					// for this pod.
					require.False(t, info.NetNSClosed)
					require.NotNil(t, info.CNIResult)
					require.NotNil(t, status.Network)
					require.NotEmpty(t, status.Network.Ip)
				}
				break
			}
		}
		for _, c := range s.containers {
			for _, loaded := range ourContainers {
				if c.id == loaded.Id {
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

func waitNoExternalSandboxes(t *testing.T, ns string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		pss, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
		if err != nil {

			time.Sleep(200 * time.Millisecond)
			if time.Now().After(deadline) {
				return false
			}
			continue
		}
		ext := 0
		for _, ps := range pss {
			md := ps.GetMetadata()
			if md == nil || md.Namespace != ns {

				if ps.State == runtime.PodSandboxState_SANDBOX_READY || ps.State == runtime.PodSandboxState_SANDBOX_NOTREADY {
					ext++
				}
			}
		}
		if ext == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

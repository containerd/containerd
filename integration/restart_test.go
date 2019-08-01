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
	"os/exec"
	"sort"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
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
					name:  "running-container",
					state: runtime.ContainerState_CONTAINER_RUNNING,
				},
				{
					name:  "exited-container",
					state: runtime.ContainerState_CONTAINER_EXITED,
				},
			},
		},
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
			_, err = task.Delete(ctx, containerd.WithProcessKill)
			require.NoError(t, err)
		}
	}

	t.Logf("Pull test images")
	for _, image := range []string{"busybox", "alpine"} {
		img, err := imageService.PullImage(&runtime.ImageSpec{image}, nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: img}))
		}()
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
	assert.Len(t, loadedContainers, len(sandboxes)*3)
	for _, s := range sandboxes {
		for _, loaded := range loadedSandboxes {
			if s.id == loaded.Id {
				assert.Equal(t, s.state, loaded.State)
				break
			}
		}
		for _, c := range s.containers {
			for _, loaded := range loadedContainers {
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

// Note: The test moves runc binary.
// The test requires:
// 1) The runtime is runc;
// 2) runc is in PATH;
func TestUnknownStateAfterContainerdRestart(t *testing.T) {
	if *runtimeHandler != "" {
		t.Skip("unsupported config: runtime handler is set")
	}
	runcPath, err := exec.LookPath("runc")
	if err != nil {
		t.Skip("unsupported config: runc not in PATH")
	}

	sbConfig := PodSandboxConfig("sandbox", "sandbox-unknown-state")

	const testImage = "busybox"
	t.Logf("Pull test image %q", testImage)
	img, err := imageService.PullImage(&runtime.ImageSpec{Image: testImage}, nil, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: img}))
	}()

	t.Log("Should not be able to create sandbox without runc")
	tmpRuncPath := Randomize(runcPath)
	require.NoError(t, os.Rename(runcPath, tmpRuncPath))
	defer func() {
		os.Rename(tmpRuncPath, runcPath)
	}()
	sb, err := runtimeService.RunPodSandbox(sbConfig, "")
	if err == nil {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
		t.Skip("unsupported config: runc is not being used")
	}
	require.NoError(t, os.Rename(tmpRuncPath, runcPath))

	t.Log("Create a sandbox")
	sb, err = runtimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)
	defer func() {
		// Make sure the sandbox is cleaned up in any case.
		runtimeService.StopPodSandbox(sb)
		runtimeService.RemovePodSandbox(sb)
	}()
	ps, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_READY, ps.GetState())

	t.Log("Create a container")
	cnConfig := ContainerConfig(
		"container-unknown-state",
		testImage,
		WithCommand("sleep", "1000"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	cs, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, cs.GetState())

	t.Log("Move runc binary, so that container/sandbox can't be loaded after restart")
	tmpRuncPath = Randomize(runcPath)
	require.NoError(t, os.Rename(runcPath, tmpRuncPath))
	defer func() {
		os.Rename(tmpRuncPath, runcPath)
	}()

	t.Log("Restart containerd")
	RestartContainerd(t)

	t.Log("Sandbox should be in NOTREADY state after containerd restart")
	ps, err = runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_NOTREADY, ps.GetState())

	t.Log("Container should be in UNKNOWN state after containerd restart")
	cs, err = runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_UNKNOWN, cs.GetState())

	t.Log("Stop/remove the sandbox should fail for the lack of runc")
	assert.Error(t, runtimeService.StopPodSandbox(sb))
	assert.Error(t, runtimeService.RemovePodSandbox(sb))

	t.Log("Stop/remove the container should fail for the lack of runc")
	assert.Error(t, runtimeService.StopContainer(cn, 10))
	assert.Error(t, runtimeService.RemoveContainer(cn))

	t.Log("Move runc back")
	require.NoError(t, os.Rename(tmpRuncPath, runcPath))

	t.Log("Sandbox should still be in NOTREADY state")
	ps, err = runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_NOTREADY, ps.GetState())

	t.Log("Container should still be in UNKNOWN state")
	cs, err = runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	assert.Equal(t, runtime.ContainerState_CONTAINER_UNKNOWN, cs.GetState())

	t.Log("Sandbox operations which require running state should fail")
	_, err = runtimeService.PortForward(&runtime.PortForwardRequest{
		PodSandboxId: sb,
		Port:         []int32{8080},
	})
	assert.Error(t, err)

	t.Log("Container operations which require running state should fail")
	assert.Error(t, runtimeService.ReopenContainerLog(cn))
	_, _, err = runtimeService.ExecSync(cn, []string{"ls"}, 10*time.Second)
	assert.Error(t, err)
	_, err = runtimeService.Attach(&runtime.AttachRequest{
		ContainerId: cn,
		Stdin:       true,
		Stdout:      true,
		Stderr:      true,
	})
	assert.Error(t, err)

	t.Log("Containerd should still be running now")
	_, err = runtimeService.Status()
	require.NoError(t, err)

	t.Log("Remove the container should fail in this state")
	assert.Error(t, runtimeService.RemoveContainer(cn))

	t.Log("Remove the sandbox should fail in this state")
	assert.Error(t, runtimeService.RemovePodSandbox(sb))

	t.Log("Should be able to stop container in this state")
	assert.NoError(t, runtimeService.StopContainer(cn, 10))

	t.Log("Should be able to stop sandbox in this state")
	assert.NoError(t, runtimeService.StopPodSandbox(sb))

	t.Log("Should be able to remove container after stop")
	assert.NoError(t, runtimeService.RemoveContainer(cn))

	t.Log("Should be able to remove sandbox after stop")
	assert.NoError(t, runtimeService.RemovePodSandbox(sb))
}

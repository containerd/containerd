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
	"errors"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestFixPlegNotready is used to verify root cause of kubelet pleg notready.
func TestFixPlegNotready(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	t.Logf("Create a pod config and run sandbox container")
	sbID, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "fix_pleg_notready")

	shimCli := connectToShim(ctx, t, sbID)

	t.Logf("Create a container config and run container in a pod")
	pauseImage := images.Get(images.Pause)
	EnsureImageExists(t, pauseImage)

	containerConfig := ContainerConfig("container1", pauseImage)
	cnID, err := runtimeService.CreateContainer(sbID, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cnID))

	delayInSec := 10
	t.Logf("[shim pid: %d]: Injecting %d seconds delay to umount2 syscall",
		shimPid(ctx, t, shimCli),
		delayInSec)

	injectDelayToUmount2(ctx, t, shimCli, delayInSec /* CRI plugin uses 10 seconds to delete task */)

	// stop the container, the shim will be blocked in deleting task
	t.Log("Stop container with timeout 1s")

	go runtimeService.StopContainer(cnID, 1)

	// make sure `umount2` present in strace's stdout
	time.Sleep(2 * time.Second)

	// list container before update resource
	t.Log("List containers before update resource with timeout 1s")
	require.NoError(t, listContainers(t, 1), "The ListContainers should be done without error")

	// update the container resource, the update will be blocked.
	// in the kubernetes, this will be called by kubelet for every 10 second by default.
	t.Log("Update container resource")
	go runtimeService.UpdateContainerResources(cnID, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: int64(64 * 1024 * 1024),
	}, nil)

	// waiting for the dead-lock happend
	time.Sleep(5 * time.Second)

	// list containers after update resource
	t.Log("List containers after update resource  with timeout 1s")
	require.NoError(t, listContainers(t, 1), "The ListContainers should be done without error with timeout 1s")
}

func listContainers(t *testing.T, timeout int) error {
	var err error
	doneCh := make(chan struct{})
	go func() {
		_, err = runtimeService.ListContainers(&runtime.ContainerFilter{})
		doneCh <- struct{}{}
	}()

	select {
	case <-time.After(time.Duration(timeout) * time.Second):
		return errors.New("deadline exceeded")
	case <-doneCh:
		return err
	}

	return nil
}

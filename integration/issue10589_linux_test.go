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
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"

	apitasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestIssue10589 is used to reproduce https://github.com/containerd/containerd/issues/10589
func TestIssue10589(t *testing.T) {
	for testIteration := 0; testIteration < 20; testIteration++ {
		t.Logf("Running %s iteration %v", t.Name(), testIteration)

		t.Log("Create a pod sandbox")
		sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", fmt.Sprintf("issue-1058-%v", testIteration))

		var (
			testImage     = images.Get(images.BusyBox)
			containerName = fmt.Sprintf("test-container-10589-%v", testIteration)
		)

		EnsureImageExists(t, testImage)

		t.Log("Create a container which sleeps")
		cnConfig := ContainerConfig(
			containerName,
			testImage,
			WithCommand("sleep", "1000"),
		)
		cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
		require.NoError(t, err)

		t.Log("Start the container")
		require.NoError(t, runtimeService.StartContainer(cn))

		t.Log("Wait for container to start running")
		require.NoError(t, Eventually(func() (bool, error) {
			s, err := runtimeService.ContainerStatus(cn)
			if err != nil {
				return false, err
			}
			if s.GetState() == runtime.ContainerState_CONTAINER_RUNNING {
				return true, nil
			}
			return false, nil
		}, time.Second, 30*time.Second))

		t.Logf("Getting container info for %s", cn)
		containerInfo, err := GetContainer(cn)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		resp, err := containerdClient.TaskService().Get(ctx, &apitasks.GetRequest{ContainerID: containerInfo.ID})
		require.NoError(t, err)

		targetPid := resp.Process.Pid
		t.Logf("Got container %s pid: %v", cn, targetPid)

		var wg sync.WaitGroup
		const numExecIterations = 1000
		for i := 1; i <= numExecIterations; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				t.Logf("Exec in container %s iteration: %v", cn, i)
				_, _, err = runtimeService.ExecSync(cn, []string{"date"}, 5*time.Second)
			}(i)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			t.Logf("Killing main pid (%v) of container %s", targetPid, cn)
			syscall.Kill(int(targetPid), syscall.SIGKILL)
		}()

		wg.Wait()

		t.Logf("Wait for container %s status to become exited", cn)
		require.NoError(t, Eventually(func() (bool, error) {
			s, err := runtimeService.ContainerStatus(cn)
			if err != nil {
				return false, err
			}
			if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
				return true, nil
			}
			return false, nil
		}, time.Second, 30*time.Second))

		t.Logf("Completed running %s iteration %v", t.Name(), testIteration)
	}
}

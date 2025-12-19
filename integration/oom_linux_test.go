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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestOOMEventMonitor(t *testing.T) {
	workDir := t.TempDir()

	rawCfg := fmt.Sprintf(`
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  runtime_path = "%s/containerd-shim-runc-v2"
`, *buildDir)

	err := os.WriteFile(
		filepath.Join(workDir, "config.toml"),
		[]byte(rawCfg),
		0600,
	)
	require.NoError(t, err, "failed to create config")

	ctrd := newCtrdProc(t, filepath.Join(*buildDir, "containerd"), workDir, []string{})

	logPath := ctrd.logPath()
	t.Cleanup(func() {
		if t.Failed() {
			dumpFileContent(t, logPath)
		}
	})
	require.NoError(t, ctrd.isReady())

	var busyboxImage = images.Get(images.BusyBox)
	pullImagesByCRI(t, ctrd.criImageService(t), busyboxImage)

	n := 8
	podCtxs := []*podTCtx{}

	defer func() {
		for _, p := range podCtxs {
			p.stop(true)
		}

		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	}()

	t.Logf("Creating %d pod sandboxes", n)
	for i := 0; i < n; i++ {
		podCtx := newPodTCtx(t,
			ctrd.criRuntimeService(t),
			fmt.Sprintf("test-oom-event-%d", i),
			"sandbox",
			WithHostNetwork)
		podCtxs = append(podCtxs, podCtx)
	}

	for round := range 10 {
		if t.Failed() {
			break
		}

		t.Logf("Creating %d running container and wait for them OOMKilled", n)
		var wg sync.WaitGroup
		for _, podCtx := range podCtxs {
			wg.Add(1)
			go func() {
				defer wg.Done()

				cntrID := podCtx.createContainer(fmt.Sprintf("round-%d-running", round), busyboxImage,
					criruntime.ContainerState_CONTAINER_RUNNING,
					WithCommand(
						"sh",
						"-c",
						"dd if=/dev/zero of=/dev/null bs=20M",
					),
					WithResources(
						&runtimeapi.LinuxContainerResources{
							MemoryLimitInBytes:     15 * 1024 * 1024,
							MemorySwapLimitInBytes: 15 * 1024 * 1024,
						},
					),
				)

				assert.NoError(t, Eventually(func() (bool, error) {
					status, err := ctrd.criRuntimeService(t).ContainerStatus(cntrID)
					if err != nil {
						return false, err
					}

					state := status.GetState()
					if state != criruntime.ContainerState_CONTAINER_EXITED {
						return false, nil
					}

					if code := status.GetExitCode(); int(code) != 137 {
						return false, fmt.Errorf("expected 137 but got %d", code)
					}

					if reason := status.GetReason(); reason != "OOMKilled" {
						return false, fmt.Errorf("expected OOMKilled but got %s", reason)
					}
					return true, nil
				}, time.Second, 90*time.Second), "wait for container %s to exit", cntrID)

				assert.NoError(t, ctrd.criRuntimeService(t).RemoveContainer(cntrID), "failed to remove container %s", cntrID)
			}()
		}
		wg.Wait()
	}
}

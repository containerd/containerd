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
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestCriContainerUpdate(t *testing.T) {
	if f := os.Getenv("RUNC_FLAVOR"); f != "" && f != "runc" {
		t.Skip("test requires runc")
	}

	workDir := t.TempDir()

	t.Log("Prepare containerd config with volatile option")
	cfgPath := filepath.Join(workDir, "config.toml")
	err := os.WriteFile(
		cfgPath,
		[]byte(`
version = 3

      [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes]
          [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
            BinaryName = '/usr/local/bin/runc-fp'
`),
		0600)
	require.NoError(t, err)

	currentProc := newCtrdProc(t, "containerd", workDir, nil)
	require.NoError(t, currentProc.isReady())
	t.Cleanup(func() {
		t.Log("Cleanup all the pods")
		cleanupPods(t, currentProc.criRuntimeService(t))
		t.Log("Stopping containerd process")
		require.NoError(t, currentProc.kill(syscall.SIGTERM))
		require.NoError(t, currentProc.wait(5*time.Minute))
	})
	cruntimeService := currentProc.criRuntimeService(t)

	var testImage = images.Get(images.BusyBox)
	pullImagesByCRI(t, currentProc.criImageService(t), testImage)

	t.Log("Create a sandbox")
	podCtx := newPodTCtx(t, currentProc.criRuntimeService(t), "sandbox-1", "sandbox")
	cnID := podCtx.createContainer(
		"container-1",
		testImage,
		runtime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"),
	)

	failPoint := "/tmp/failpoint_profile.json"
	err = os.WriteFile(
		failPoint,
		[]byte(`
{
  "failpoint": "delayUpdate"
}
`), 0600)
	require.NoError(t, err)
	defer func() {
		os.Remove(failPoint)
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func(t *testing.T) {
		t0 := time.Now()
		cruntimeService.UpdateContainerResources(cnID, &runtime.LinuxContainerResources{
			MemoryLimitInBytes: int64(256 * 1024 * 1024),
		}, nil)
		t.Logf("update container use %v", time.Since(t0))
		wg.Done()
	}(t)
	// make sure go func running
	time.Sleep(time.Millisecond * 500)
	t1 := time.Now()
	_, err = cruntimeService.ContainerStatus(cnID)
	assert.NoError(t, err)
	duration := time.Since(t1)
	wg.Wait()
	if duration > 1*time.Second {
		t.Fatalf("get container  status use %v", duration)
	}
}

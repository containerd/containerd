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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestCriContainerOOM(t *testing.T) {
	if f := os.Getenv("RUNC_FLAVOR"); f != "" && f != "runc" {
		t.Skip("test requires runc")
	}

	var cfgPath string
	var cgroupManager string
	workDir := t.TempDir()
	if f := os.Getenv("CGROUP_DRIVER"); f != "" && f == "systemd" {
		t.Log("Prepare containerd config with systemd driver")
		cfgPath = filepath.Join(workDir, "config.toml")
		err := os.WriteFile(
			cfgPath,
			[]byte(`
version = 3
		  [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes]
			  [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
				SystemdCgroup = true
	`),
			0600)
		require.NoError(t, err)
		cgroupManager = "/test.slice"
	} else {
		t.Log("Prepare containerd config with cgroupfs driver")
		cfgPath = filepath.Join(workDir, "config.toml")
		err := os.WriteFile(
			cfgPath,
			[]byte(`
	version = 3
		  [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes]
			  [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
				SystemdCgroup = false
	`),
			0600)
		require.NoError(t, err)
	}

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
	oomContainers := map[string]struct{}{}
	oomContainerList := []string{}
	var wgCreate sync.WaitGroup
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wgCreate.Add(1)
		go func(t *testing.T) {
			defer wgCreate.Done()

			podCtx := newPodTCtx(t, currentProc.criRuntimeService(t), fmt.Sprintf("sandbox_%d", i), "sandbox", WithCgroupParent(cgroupManager))
			cnConfig := ContainerConfig(
				fmt.Sprintf("container_oom_test_%d", i),
				testImage,
				WithResources(&runtime.LinuxContainerResources{
					MemoryLimitInBytes:     15 * 1024 * 1024,
					MemorySwapLimitInBytes: 15 * 1024 * 1024,
				}),
				WithCommand("dd", "if=/dev/zero", "of=/dev/null", "bs=20M"),
			)
			cid := podCtx.createContainerWithConfig(
				cnConfig,
				runtime.ContainerState_CONTAINER_RUNNING,
			)
			mu.Lock()
			oomContainerList = append(oomContainerList, cid)
			oomContainers[cid] = struct{}{}
			mu.Unlock()
		}(t)
	}

	wgCreate.Wait()
	for _, k := range oomContainerList {
		wg.Add(1)
		go func(t *testing.T) {
			defer wg.Done()
			for {
				status, err := cruntimeService.ContainerStatus(k)
				require.NoError(t, err)
				if status.ExitCode != 137 {
					time.Sleep(time.Second * 1)
					continue
				}
				if strings.Contains(status.Reason, "OOMKilled") {
					mu.Lock()
					delete(oomContainers, k)
					mu.Unlock()
					break
				}
				time.Sleep(time.Second * 1)
			}
		}(t)
	}

	waitDone := make(chan int)
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-time.After(60 * time.Second):
		t.Fatalf("time out leak oom containers %#v", oomContainers)
	case <-waitDone:
	}
}

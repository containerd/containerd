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
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestRunContainerWithVolatileOption(t *testing.T) {
	if ok, err := kernelversion.GreaterEqualThan(kernelversion.KernelVersion{
		Kernel: 5,
		Major:  10,
	}); !ok {
		t.Skipf("Only test it when kernel >= 5.10: %v", err)
	}

	workDir := t.TempDir()

	t.Log("Prepare containerd config with volatile option")
	cfgPath := filepath.Join(workDir, "config.toml")
	err := os.WriteFile(
		cfgPath,
		[]byte(`
version = 3

[plugins.'io.containerd.internal.v1.cri']
  ignore_image_defined_volumes = false

[plugins."io.containerd.snapshotter.v1.overlayfs"]
    mount_options = ["volatile"]
`),
		0600)
	require.NoError(t, err)

	t.Logf("Starting containerd")
	currentProc := newCtrdProc(t, "containerd", workDir, nil)
	require.NoError(t, currentProc.isReady())
	t.Cleanup(func() {
		t.Log("Cleanup all the pods")
		cleanupPods(t, currentProc.criRuntimeService(t))

		t.Log("Stopping containerd process")
		require.NoError(t, currentProc.kill(syscall.SIGTERM))
		require.NoError(t, currentProc.wait(5*time.Minute))
	})

	imageName := images.Get(images.VolumeOwnership)
	pullImagesByCRI(t, currentProc.criImageService(t), imageName)

	podCtx := newPodTCtx(t, currentProc.criRuntimeService(t), "running-pod", "sandbox")

	podCtx.createContainer("running", imageName,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"))
}

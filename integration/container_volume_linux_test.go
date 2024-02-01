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
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func testReadonlyMounts(t *testing.T, mode string, expectRRO bool) {
	workDir := t.TempDir()
	mntSrcDir := filepath.Join(workDir, "mnt") // "/mnt" in the container
	require.NoError(t, os.MkdirAll(mntSrcDir, 0755))
	tmpfsDir := filepath.Join(mntSrcDir, "tmpfs") // "/mnt/tmpfs" in the container
	require.NoError(t, os.MkdirAll(tmpfsDir, 0755))
	tmpfsMount := mount.Mount{
		Type:   "tmpfs",
		Source: "none",
	}
	require.NoError(t, tmpfsMount.Mount(tmpfsDir))
	t.Cleanup(func() {
		require.NoError(t, mount.UnmountAll(tmpfsDir, 0))
	})

	podLogDir := filepath.Join(workDir, "podLogDir")
	require.NoError(t, os.MkdirAll(podLogDir, 0755))

	config := `version = 2
`
	if mode != "" {
		config += fmt.Sprintf(`
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  treat_ro_mount_as_rro = %q
`, mode)
	}
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.toml"),
		[]byte(config), 0644))
	ctrdProc := newCtrdProc(t, "containerd", workDir)
	t.Cleanup(func() {
		cleanupPods(t, ctrdProc.criRuntimeService(t))
		require.NoError(t, ctrdProc.kill(syscall.SIGTERM))
		require.NoError(t, ctrdProc.wait(5*time.Minute))
		if t.Failed() {
			dumpFileContent(t, ctrdProc.logPath())
		}
	})
	runtimeServiceOrig, imageServiceOrig := runtimeService, imageService
	runtimeService, imageService = ctrdProc.criRuntimeService(t), ctrdProc.criImageService(t)
	t.Cleanup(func() {
		runtimeService, imageService = runtimeServiceOrig, imageServiceOrig
	})
	require.NoError(t, ctrdProc.isReady())

	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "test-ro-mounts",
		WithPodLogDirectory(podLogDir),
	)

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	containerName := "test-container"
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("/bin/touch", "/mnt/tmpfs/file"),
		WithLogPath(containerName),
		func(c *runtime.ContainerConfig) {
			c.Mounts = append(c.Mounts, &runtime.Mount{
				HostPath:       mntSrcDir,
				ContainerPath:  "/mnt",
				SelinuxRelabel: selinux.GetEnabled(),
				Readonly:       true,
			})
		},
	)

	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for container to finish running")
	exitCode := -1
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			exitCode = int(s.ExitCode)
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	output, err := os.ReadFile(filepath.Join(podLogDir, containerName))
	assert.NoError(t, err)
	t.Logf("exitCode=%d, output=%q", exitCode, output)

	if expectRRO {
		require.NotEqual(t, 0, exitCode)
		require.Contains(t, string(output), "stderr F touch: /mnt/tmpfs/file: Read-only file system\n")
	} else {
		require.Equal(t, 0, exitCode)
	}
}

func TestReadonlyMounts(t *testing.T) {
	kernelSupportsRRO, err := kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 5, Major: 12})
	require.NoError(t, err)
	t.Run("Default", func(t *testing.T) {
		testReadonlyMounts(t, "", kernelSupportsRRO)
	})
	t.Run("Disabled", func(t *testing.T) {
		testReadonlyMounts(t, "Disabled", false)
	})
	if kernelSupportsRRO {
		t.Run("Enabled", func(t *testing.T) {
			testReadonlyMounts(t, "Enabled", true)
		})
	}
}

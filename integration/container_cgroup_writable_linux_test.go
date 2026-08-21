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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/integration/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func newContainerdProcess(t *testing.T, cgroupWritable bool) *ctrdProc {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	config := fmt.Sprintf(`
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  cgroup_writable = %t
`,
		cgroupWritable)

	err := os.WriteFile(configPath, []byte(config), 0600)
	require.NoError(t, err)

	currentProc := newCtrdProc(t, "containerd", configDir, nil)
	require.NoError(t, currentProc.isReady())

	return currentProc
}

func TestContainerCgroupWritable(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2")
	}

	testCases := []struct {
		name           string
		cgroupWritable bool
	}{
		{
			name:           "writable cgroup",
			cgroupWritable: true,
		},
		{
			name:           "readonly cgroup",
			cgroupWritable: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			currentProc := newContainerdProcess(t, testCase.cgroupWritable)

			// Get the runtime service
			runtimeService, err := remote.NewRuntimeService(currentProc.grpcAddress(), 1*time.Minute)
			require.NoError(t, err)

			t.Cleanup(func() {
				cleanupPods(t, runtimeService)
				assert.NoError(t, runtimeService.Close(context.Background()))
				t.Log("Stopping containerd process")
				require.NoError(t, currentProc.kill(syscall.SIGTERM))
				require.NoError(t, currentProc.wait(5*time.Minute))
			})

			imageName := images.Get(images.BusyBox)
			pullImagesByCRI(t, currentProc.criImageService(t), imageName)

			// Create a test sandbox
			sbConfig := &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "sandbox",
					Namespace: "cgroup-writable",
				},
			}
			sb, err := runtimeService.RunPodSandbox(sbConfig, "")
			require.NoError(t, err)

			containerName := "cgroup-writable-test"
			cnConfig := &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name: containerName,
				},
				Image: &runtime.ImageSpec{
					Image: imageName,
				},
				Command: []string{"sh", "-c", "sleep 1d"},
			}

			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, runtimeService.RemoveContainer(cn))
			}()

			require.NoError(t, runtimeService.StartContainer(cn))
			defer func() {
				assert.NoError(t, runtimeService.StopContainer(cn, 30))
			}()

			status, err := runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			assert.Equal(t, status.GetState(), runtime.ContainerState_CONTAINER_RUNNING)

			// Execute a command to verify if cgroup is writable
			_, stderr, err := runtimeService.ExecSync(cn, []string{"mkdir", "sys/fs/cgroup/dummy-group"}, 2)
			if testCase.cgroupWritable {
				require.NoError(t, err)
				require.Empty(t, stderr)
			} else {
				require.Error(t, err)
				require.Contains(t, string(stderr), "mkdir: can't create directory 'sys/fs/cgroup/dummy-group': Read-only file system")
			}
		})
	}
}

func TestContainerCgroupMountMode(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2")
	}

	mountInfo, err := mount.Lookup("/sys/fs/cgroup")
	require.NoError(t, err)
	if !slices.Contains(strings.Split(mountInfo.VFSOptions, ","), "nsdelegate") {
		t.Skip("requires /sys/fs/cgroup to be mounted with nsdelegate")
	}

	testCases := []struct {
		name      string
		ns        string
		mountMode runtime.CgroupMountMode
		writable  bool
	}{
		{
			name:      "writable cgroup",
			ns:        "cgroup-mount-mode-writable",
			mountMode: runtime.CgroupMountMode_CGROUP_MOUNT_MODE_WRITABLE,
			writable:  true,
		},
		{
			name:      "readonly cgroup",
			ns:        "cgroup-mount-mode-readonly",
			mountMode: runtime.CgroupMountMode_CGROUP_MOUNT_MODE_READ_ONLY,
			writable:  false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Log("Create a pod sandbox")
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", testCase.ns)

			testImage := images.Get(images.BusyBox)
			EnsureImageExists(t, testImage)

			t.Log("Create a container requesting the cgroup mount mode")
			cnConfig := ContainerConfig(
				"cgroup-mount-mode-test",
				testImage,
				WithCommand("sh", "-c", "sleep 1d"),
				WithCgroupMountMode(testCase.mountMode),
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, runtimeService.RemoveContainer(cn))
			}()

			require.NoError(t, runtimeService.StartContainer(cn))
			defer func() {
				assert.NoError(t, runtimeService.StopContainer(cn, 30))
			}()

			_, stderr, err := runtimeService.ExecSync(cn, []string{"mkdir", "/sys/fs/cgroup/dummy-group"}, 2*time.Second)
			if testCase.writable {
				require.NoError(t, err)
				require.Empty(t, stderr)
			} else {
				require.Error(t, err)
				require.Contains(t, string(stderr), "Read-only file system")
			}
		})
	}
}

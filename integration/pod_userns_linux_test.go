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

	"github.com/containerd/containerd/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	exec "golang.org/x/sys/execabs"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPodUserNS(t *testing.T) {
	containerID := uint32(0)
	hostID := uint32(65536)
	size := uint32(65536)
	for name, test := range map[string]struct {
		sandboxOpts   []PodSandboxOpts
		containerOpts []ContainerOpts
		checkOutput   func(t *testing.T, output string)
		expectErr     bool
	}{
		"userns uid mapping": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				WithCommand("cat", "/proc/self/uid_map"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The output should contain the length of the userns requested.
				assert.Contains(t, output, fmt.Sprint(size))
			},
		},
		"userns gid mapping": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				WithCommand("cat", "/proc/self/gid_map"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The output should contain the length of the userns requested.
				assert.Contains(t, output, fmt.Sprint(size))
			},
		},
		"rootfs permissions": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				// Prints numeric UID and GID for path.
				// For example, if UID and GID is 0 it will print: =0=0=
				// We add the "=" signs so we use can assert.Contains() and be sure
				// the UID/GID is 0 and not things like 100 (that contain 0).
				// We can't use assert.Equal() easily as it contains timestamp, etc.
				WithCommand("stat", "-c", "'=%u=%g='", "/root/"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The UID and GID should be 0 (root) if the chown/remap is done correctly.
				assert.Contains(t, output, "=0=0=")
			},
		},
		"fails with several mappings": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
				WithPodUserNs(containerID*2, hostID*2, size*2),
			},
			expectErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("true")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags: syscall.CLONE_NEWUSER,
			}
			if err := cmd.Run(); err != nil {
				t.Skip("skipping test: user namespaces are unavailable")
			}

			testPodLogDir := t.TempDir()
			sandboxOpts := append(test.sandboxOpts, WithPodLogDirectory(testPodLogDir))
			t.Log("Create a sandbox with userns")
			sbConfig := PodSandboxConfig("sandbox", "userns", sandboxOpts...)
			sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
			if err != nil {
				if !test.expectErr {
					t.Fatalf("Unexpected RunPodSandbox error: %v", err)
				}
				return
			}
			// Make sure the sandbox is cleaned up.
			defer func() {
				assert.NoError(t, runtimeService.StopPodSandbox(sb))
				assert.NoError(t, runtimeService.RemovePodSandbox(sb))
			}()
			if test.expectErr {
				t.Fatalf("Expected RunPodSandbox to return error")
			}

			var (
				testImage     = images.Get(images.BusyBox)
				containerName = "test-container"
			)

			EnsureImageExists(t, testImage)

			containerOpts := append(test.containerOpts,
				WithLogPath(containerName),
			)
			t.Log("Create a container for userns")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				containerOpts...,
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)

			t.Log("Start the container")
			require.NoError(t, runtimeService.StartContainer(cn))

			t.Log("Wait for container to finish running")
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

			content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
			assert.NoError(t, err)

			t.Log("Running check function")
			test.checkOutput(t, string(content))
		})
	}
}

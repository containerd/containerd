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
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPodHostname(t *testing.T) {
	hostname, err := os.Hostname()
	require.NoError(t, err)
	for name, test := range map[string]struct {
		opts             []PodSandboxOpts
		expectedHostname string
		expectErr        bool
		needsHostNetwork bool
	}{
		"regular pod with custom hostname": {
			opts: []PodSandboxOpts{
				WithPodHostname("test-hostname"),
			},
			expectedHostname: "test-hostname",
		},
		"host network pod without custom hostname": {
			opts: []PodSandboxOpts{
				WithHostNetwork,
			},
			expectedHostname: hostname,
			needsHostNetwork: true,
		},
		"host network pod with custom hostname should fail": {
			opts: []PodSandboxOpts{
				WithHostNetwork,
				WithPodHostname("test-hostname"),
			},
			expectErr:        true,
			needsHostNetwork: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if test.needsHostNetwork && goruntime.GOOS == "windows" {
				t.Skip("Skipped on Windows.")
			}
			testPodLogDir := t.TempDir()

			opts := append(test.opts, WithPodLogDirectory(testPodLogDir))
			t.Log("Create a sandbox with hostname")
			sbConfig := PodSandboxConfig("sandbox", "hostname", opts...)
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

			t.Log("Create a container to print env")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("sh", "-c",
					"echo -n /etc/hostname= && hostname && env"),
				WithLogPath(containerName),
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

			t.Log("Search hostname env in container log")
			if goruntime.GOOS == "windows" {
				assert.Contains(t, string(content), "COMPUTERNAME="+strings.ToUpper(test.expectedHostname))
			} else {
				assert.Contains(t, string(content), "HOSTNAME="+test.expectedHostname)
			}

			t.Log("Search /etc/hostname content in container log")
			assert.Contains(t, string(content), "/etc/hostname="+test.expectedHostname)
		})
	}
}

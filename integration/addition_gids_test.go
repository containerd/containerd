//go:build linux

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
	"testing"
	"time"

	"github.com/containerd/containerd/integration/images"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestAdditionalGids(t *testing.T) {
	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)
	type testCase struct {
		description string
		opts        []ContainerOpts
		expected    string
	}

	testCases := []testCase{
		{
			description: "Equivalent of `docker run` (no option)",
			opts:        nil,
			expected:    "groups=0(root),10(wheel)",
		},
		{
			description: "Equivalent of `docker run --group-add 1 --group-add 1234`",
			opts:        []ContainerOpts{WithSupplementalGroups([]int64{1 /*daemon*/, 1234 /*new group*/})},
			expected:    "groups=0(root),1(daemon),10(wheel),1234",
		},
		{
			description: "Equivalent of `docker run --user 1234`",
			opts:        []ContainerOpts{WithRunAsUser(1234)},
			expected:    "groups=0(root)",
		},
		{
			description: "Equivalent of `docker run --user 1234:1234`",
			opts:        []ContainerOpts{WithRunAsUser(1234), WithRunAsGroup(1234)},
			expected:    "groups=1234",
		},
		{
			description: "Equivalent of `docker run --user 1234 --group-add 1234`",
			opts:        []ContainerOpts{WithRunAsUser(1234), WithSupplementalGroups([]int64{1234})},
			expected:    "groups=0(root),1234",
		},
		{
			description: "Equivalent of `docker run --user daemon` (Supported by CRI, although unsupported by kube-apiserver)",
			opts:        []ContainerOpts{WithRunAsUsername("daemon")},
			expected:    "groups=1(daemon)",
		},
		{
			description: "Equivalent of `docker run --user daemon --group-add 1234` (Supported by CRI, although unsupported by kube-apiserver)",
			opts:        []ContainerOpts{WithRunAsUsername("daemon"), WithSupplementalGroups([]int64{1234})},
			expected:    "groups=1(daemon),1234",
		},
	}

	for i, tc := range testCases {
		i, tc := i, tc
		tBasename := fmt.Sprintf("case-%d", i)
		t.Run(tBasename, func(t *testing.T) {
			t.Log(tc.description)
			t.Logf("Expected=%q", tc.expected)

			testPodLogDir := t.TempDir()

			t.Log("Create a sandbox with log directory")
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", tBasename,
				WithPodLogDirectory(testPodLogDir))

			t.Log("Create a container to print id")
			containerName := tBasename
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				append(
					[]ContainerOpts{
						WithCommand("id"),
						WithLogPath(containerName),
					}, tc.opts...)...,
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

			t.Log("Search additional groups in container log")
			content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
			assert.NoError(t, err)
			assert.Contains(t, string(content), tc.expected+"\n")
		})
	}
}

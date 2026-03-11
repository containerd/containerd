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
	"strings"
	"testing"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrivilegedContainerCgroupMountOptions(t *testing.T) {
	if f := os.Getenv("RUNC_FLAVOR"); f == "crun" {
		t.Skip("Skipping until crun supports cgroup v2 mount options (https://github.com/containers/crun/pull/2040)")
	}
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("Requires cgroup v2")
	}

	hostMountBefore, err := mount.Lookup("/sys/fs/cgroup")
	require.NoError(t, err)

	if !strings.Contains(hostMountBefore.VFSOptions, "nsdelegate") && !strings.Contains(hostMountBefore.VFSOptions, "memory_recursiveprot") {
		t.Skip("requires host cgroup mount to have nsdelegate or memory_recursiveprot")
	}

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a sandbox with privileged=true")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "privileged-cgroup-mount-test", WithPodSecurityContext(true))

	t.Log("Create a container with privileged=true")
	cnConfig := ContainerConfig("container", testImage, WithCommand("sh", "-c", "sleep 1d"), WithSecurityContext(true))
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := runtimeService.RemoveContainer(cn); err != nil {
			t.Logf("failed to remove container %s: %v", cn, err)
		}
	})

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	t.Cleanup(func() {
		if err := runtimeService.StopContainer(cn, 10); err != nil {
			t.Logf("failed to stop container %s: %v", cn, err)
		}
	})

	hostMountAfter, err := mount.Lookup("/sys/fs/cgroup")
	require.NoError(t, err)

	if strings.Contains(hostMountBefore.VFSOptions, "nsdelegate") {
		assert.Contains(t, hostMountAfter.VFSOptions, "nsdelegate", "nsdelegate should be preserved on the host cgroup mount")
	}
	if strings.Contains(hostMountBefore.VFSOptions, "memory_recursiveprot") {
		assert.Contains(t, hostMountAfter.VFSOptions, "memory_recursiveprot", "memory_recursiveprot should be preserved on the host cgroup mount")
	}
}

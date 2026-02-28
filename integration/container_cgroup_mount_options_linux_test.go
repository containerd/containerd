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
	"runtime"
	"strings"
	"testing"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrivilegedContainerCgroupMountOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Doesn't apply to windows")
	}
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2")
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
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	hostMountAfter, err := mount.Lookup("/sys/fs/cgroup")
	require.NoError(t, err)

	require.Equal(t, hostMountBefore.VFSOptions, hostMountAfter.VFSOptions, "host cgroup mount options changed after starting privileged container")
}

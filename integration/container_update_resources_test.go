// +build linux

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
	"testing"

	"github.com/containerd/cgroups"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func checkMemoryLimit(t *testing.T, spec *runtimespec.Spec, memLimit int64) {
	require.NotNil(t, spec)
	require.NotNil(t, spec.Linux)
	require.NotNil(t, spec.Linux.Resources)
	require.NotNil(t, spec.Linux.Resources.Memory)
	require.NotNil(t, spec.Linux.Resources.Memory.Limit)
	assert.Equal(t, memLimit, *spec.Linux.Resources.Memory.Limit)
}

func TestUpdateContainerResources(t *testing.T) {
	// TODO(claudiub): Make this test work once https://github.com/microsoft/hcsshim/pull/931 merges.
	t.Log("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "update-container-resources")
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

	EnsureImageExists(t, pauseImage)

	t.Log("Create a container with memory limit")
	cnConfig := ContainerConfig(
		"container",
		pauseImage,
		WithResources(&runtime.LinuxContainerResources{
			MemoryLimitInBytes: 200 * 1024 * 1024,
		}),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	container, err := containerdClient.LoadContainer(context.Background(), cn)
	require.NoError(t, err)
	spec, err := container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 200*1024*1024)

	t.Log("Update container memory limit after created")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 400 * 1024 * 1024,
	})
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 400*1024*1024)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	task, err := container.Task(context.Background(), nil)
	require.NoError(t, err)

	t.Log("Check memory limit in cgroup")
	cgroup, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(task.Pid())))
	require.NoError(t, err)
	stat, err := cgroup.Stat(cgroups.IgnoreNotExist)
	require.NoError(t, err)
	assert.Equal(t, uint64(400*1024*1024), stat.Memory.Usage.Limit)

	t.Log("Update container memory limit after started")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 800 * 1024 * 1024,
	})
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 800*1024*1024)

	t.Log("Check memory limit in cgroup")
	stat, err = cgroup.Stat(cgroups.IgnoreNotExist)
	require.NoError(t, err)
	assert.Equal(t, uint64(800*1024*1024), stat.Memory.Usage.Limit)
}

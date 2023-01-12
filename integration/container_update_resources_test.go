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
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/integration/images"
	criopts "github.com/containerd/containerd/pkg/cri/opts"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func checkMemoryLimit(t *testing.T, spec *runtimespec.Spec, memLimit int64) {
	require.NotNil(t, spec)
	require.NotNil(t, spec.Linux)
	require.NotNil(t, spec.Linux.Resources)
	require.NotNil(t, spec.Linux.Resources.Memory)
	require.NotNil(t, spec.Linux.Resources.Memory.Limit)
	assert.Equal(t, memLimit, *spec.Linux.Resources.Memory.Limit)
}

func checkMemorySwapLimit(t *testing.T, spec *runtimespec.Spec, memLimit *int64) {
	require.NotNil(t, spec)
	require.NotNil(t, spec.Linux)
	require.NotNil(t, spec.Linux.Resources)
	require.NotNil(t, spec.Linux.Resources.Memory)
	if memLimit == nil {
		require.Nil(t, spec.Linux.Resources.Memory.Swap)
	} else {
		require.NotNil(t, spec.Linux.Resources.Memory.Swap)
		assert.Equal(t, *memLimit, *spec.Linux.Resources.Memory.Swap)
	}
}

func checkMemoryLimitInContainerStatus(t *testing.T, status *runtime.ContainerStatus, memLimit int64) {
	t.Helper()
	require.NotNil(t, status)
	require.NotNil(t, status.Resources)
	require.NotNil(t, status.Resources.Linux)
	require.NotNil(t, status.Resources.Linux.MemoryLimitInBytes)
	assert.Equal(t, memLimit, status.Resources.Linux.MemoryLimitInBytes)
}

func getCgroupSwapLimitForTask(t *testing.T, task containerd.Task) uint64 {
	if cgroups.Mode() == cgroups.Unified {
		groupPath, err := cgroupsv2.PidGroupPath(int(task.Pid()))
		if err != nil {
			t.Fatal(err)
		}
		cgroup2, err := cgroupsv2.Load(groupPath)
		if err != nil {
			t.Fatal(err)
		}
		stat, err := cgroup2.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return stat.Memory.SwapLimit + stat.Memory.UsageLimit
	}
	cgroup, err := cgroup1.Load(cgroup1.PidPath(int(task.Pid())))
	if err != nil {
		t.Fatal(err)
	}
	stat, err := cgroup.Stat(cgroup1.IgnoreNotExist)
	if err != nil {
		t.Fatal(err)
	}
	return stat.Memory.HierarchicalSwapLimit
}

func getCgroupMemoryLimitForTask(t *testing.T, task containerd.Task) uint64 {
	if cgroups.Mode() == cgroups.Unified {
		groupPath, err := cgroupsv2.PidGroupPath(int(task.Pid()))
		if err != nil {
			t.Fatal(err)
		}
		cgroup2, err := cgroupsv2.Load(groupPath)
		if err != nil {
			t.Fatal(err)
		}
		stat, err := cgroup2.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return stat.Memory.UsageLimit
	}

	cgroup, err := cgroup1.Load(cgroup1.PidPath(int(task.Pid())))
	if err != nil {
		t.Fatal(err)
	}
	stat, err := cgroup.Stat(cgroup1.IgnoreNotExist)
	if err != nil {
		t.Fatal(err)
	}
	return stat.Memory.Usage.Limit
}

func isSwapLikelyEnabled() bool {
	// Check whether swap is enabled.
	swapFile := "/proc/swaps"
	swapData, err := os.ReadFile(swapFile)
	if err != nil {
		// We can't read the file or it doesn't exist, assume we don't have swap.
		return false
	}

	swapData = bytes.TrimSpace(swapData) // extra trailing \n
	swapLines := strings.Split(string(swapData), "\n")

	// If there is more than one line (table headers) in /proc/swaps, swap is enabled
	if len(swapLines) <= 1 {
		return false
	}

	// Linux Kernel's prior to 5.8 can disable swap accounting and is disabled
	// by default on Ubuntu. Most systems that run with cgroupsv2 enabled likely
	// have swap accounting enabled, here we assume that is true when running with
	// cgroupsv2 and check on cgroupsv1.
	if cgroups.Mode() == cgroups.Unified {
		return true
	}

	_, err = os.Stat("/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes")
	// Assume any error means this test can't run for now.
	return err == nil
}

func TestUpdateContainerResources_MemorySwap(t *testing.T) {
	if !isSwapLikelyEnabled() {
		t.Skipf("Swap or swap accounting are not enabled. Swap is required for this test")
		return
	}

	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "update-container-swap-resources")

	pauseImage := images.Get(images.Pause)
	EnsureImageExists(t, pauseImage)

	memoryLimit := int64(128 * 1024 * 1024)
	baseSwapLimit := int64(200 * 1024 * 1024)
	increasedSwapLimit := int64(256 * 1024 * 1024)

	expectedBaseSwap := baseSwapLimit
	expectedIncreasedSwap := increasedSwapLimit

	t.Log("Create a container with memory limit but no swap")
	cnConfig := ContainerConfig(
		"container",
		pauseImage,
		WithResources(&runtime.LinuxContainerResources{
			MemoryLimitInBytes:     memoryLimit,
			MemorySwapLimitInBytes: baseSwapLimit,
		}),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	container, err := containerdClient.LoadContainer(context.Background(), cn)
	require.NoError(t, err)
	spec, err := container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, memoryLimit)
	checkMemorySwapLimit(t, spec, &expectedBaseSwap)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	sw1 := baseSwapLimit
	checkMemorySwapLimit(t, spec, &sw1)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	task, err := container.Task(context.Background(), nil)
	require.NoError(t, err)

	t.Log("Check memory limit in cgroup")
	memLimit := getCgroupMemoryLimitForTask(t, task)
	swapLimit := getCgroupSwapLimitForTask(t, task)
	assert.Equal(t, uint64(memoryLimit), memLimit)
	assert.Equal(t, uint64(expectedBaseSwap), swapLimit)

	t.Log("Update container memory limit after started")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemorySwapLimitInBytes: increasedSwapLimit,
	}, nil)
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemorySwapLimit(t, spec, &increasedSwapLimit)

	t.Log("Check memory limit in cgroup")
	swapLimit = getCgroupSwapLimitForTask(t, task)
	assert.Equal(t, uint64(expectedIncreasedSwap), swapLimit)
}

func TestUpdateContainerResources_MemoryLimit(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "update-container-resources")

	pauseImage := images.Get(images.Pause)
	EnsureImageExists(t, pauseImage)

	expectedSwapLimit := func(memoryLimit int64) *int64 {
		if criopts.SwapControllerAvailable() {
			return &memoryLimit
		}
		return nil
	}

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
	checkMemorySwapLimit(t, spec, expectedSwapLimit(200*1024*1024))

	t.Log("Update container memory limit after created")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 400 * 1024 * 1024,
	}, nil)
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 400*1024*1024)
	checkMemorySwapLimit(t, spec, expectedSwapLimit(400*1024*1024))

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	task, err := container.Task(context.Background(), nil)
	require.NoError(t, err)

	t.Log("Check memory limit in cgroup")
	memLimit := getCgroupMemoryLimitForTask(t, task)
	assert.Equal(t, uint64(400*1024*1024), memLimit)
	if criopts.SwapControllerAvailable() {
		swapLimit := getCgroupSwapLimitForTask(t, task)
		assert.Equal(t, uint64(400*1024*1024), swapLimit)
	}

	t.Log("Update container memory limit after started")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 800 * 1024 * 1024,
	}, nil)
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 800*1024*1024)
	checkMemorySwapLimit(t, spec, expectedSwapLimit(800*1024*1024))

	t.Log("Check memory limit in cgroup")
	memLimit = getCgroupMemoryLimitForTask(t, task)
	assert.Equal(t, uint64(800*1024*1024), memLimit)
	if criopts.SwapControllerAvailable() {
		swapLimit := getCgroupSwapLimitForTask(t, task)
		assert.Equal(t, uint64(800*1024*1024), swapLimit)
	}

}

func TestUpdateContainerResources_StatusUpdated(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "update-container-resources")

	pauseImage := images.Get(images.Pause)
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

	t.Log("Check memory limit in container status")
	status, err := runtimeService.ContainerStatus(cn)
	checkMemoryLimitInContainerStatus(t, status, 200*1024*1024)
	require.NoError(t, err)

	t.Log("Update container memory limit after created")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 400 * 1024 * 1024,
	}, nil)
	require.NoError(t, err)

	t.Log("Check memory limit in container status")
	status, err = runtimeService.ContainerStatus(cn)
	checkMemoryLimitInContainerStatus(t, status, 400*1024*1024)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Update container memory limit after started")
	err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 800 * 1024 * 1024,
	}, nil)
	require.NoError(t, err)

	t.Log("Check memory limit in container status")
	status, err = runtimeService.ContainerStatus(cn)
	checkMemoryLimitInContainerStatus(t, status, 800*1024*1024)
	require.NoError(t, err)
}

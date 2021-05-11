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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func checkMemoryLimit(t *testing.T, spec *runtimespec.Spec, memLimit int64) {
	require.NotNil(t, spec)
	if goruntime.GOOS == "windows" {
		require.NotNil(t, spec.Windows)
		require.NotNil(t, spec.Windows.Resources)
		require.NotNil(t, spec.Windows.Resources.Memory)
		require.NotNil(t, spec.Windows.Resources.Memory.Limit)
		assert.Equal(t, uint64(memLimit), *spec.Windows.Resources.Memory.Limit)
	} else {
		require.NotNil(t, spec.Linux)
		require.NotNil(t, spec.Linux.Resources)
		require.NotNil(t, spec.Linux.Resources.Memory)
		require.NotNil(t, spec.Linux.Resources.Memory.Limit)
		assert.Equal(t, memLimit, *spec.Linux.Resources.Memory.Limit)
	}
}

func checkCPULimit(t *testing.T, spec *runtimespec.Spec, cpuLimit int64) {
	require.NotNil(t, spec)
	if goruntime.GOOS == "windows" {
		require.NotNil(t, spec.Windows)
		require.NotNil(t, spec.Windows.Resources)
		require.NotNil(t, spec.Windows.Resources.CPU)
		require.NotNil(t, spec.Windows.Resources.CPU.Maximum)
		assert.Equal(t, cpuLimit/int64(goruntime.NumCPU()), *spec.Windows.Resources.CPU.Maximum)
	} else {
		require.NotNil(t, spec.Linux)
		require.NotNil(t, spec.Linux.Resources)
		require.NotNil(t, spec.Linux.Resources.CPU)
		require.NotNil(t, spec.Linux.Resources.CPU.Period)
		require.NotNil(t, spec.Linux.Resources.CPU.Quota)
		assert.Equal(t, uint64(10000), *spec.Linux.Resources.CPU.Period)
		assert.Equal(t, cpuLimit, *spec.Linux.Resources.CPU.Quota)
	}
}

func TestUpdateContainerResources_MemoryLimit(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "update-container-resources")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 250 MB memory for 90 seconds.
	var resourceLimits, command ContainerOpts
	if goruntime.GOOS == "windows" {
		// -d: Leak and touch memory in specified MBs
		// -c: Count of number of objects to allocate
		command = WithCommand("testlimit.exe", "-accepteula", "-d", "1", "-c", "250")
		resourceLimits = WithWindowsResources(&runtime.WindowsContainerResources{
			MemoryLimitInBytes: 100 * 1024 * 1024,
		})
	} else {
		// -m: spawn N workers spinning on malloc()/free()
		// --vm-bytes: malloc B bytes per vm worker (default is 256MB)
		// NOTE(claudiub): stress will error out if it's unable to allocate all the memory it needs.
		// So, we consume 95 MB and try to consume more afterwards.
		command = WithCommand("sh", "-c", "stress -m 145 --vm-bytes 1M --vm-hang 0 -t 90 & while true; do stress -m 50 --vm-bytes 1M --vm-hang 0 -t 90; done")
		resourceLimits = WithResources(&runtime.LinuxContainerResources{
			MemoryLimitInBytes: 100 * 1024 * 1024,
		})
	}

	t.Log("Create a container with memory limit")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		resourceLimits,
		command,
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Check memory limit in container OCI spec")
	container, err := containerdClient.LoadContainer(context.Background(), cn)
	require.NoError(t, err)
	spec, err := container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 100*1024*1024)

	t.Log("Update container memory limit after created")
	if goruntime.GOOS == "windows" {
		err = runtimeService.UpdateContainerResources(cn, nil, &runtime.WindowsContainerResources{
			MemoryLimitInBytes: 150 * 1024 * 1024,
		})
	} else {
		err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
			MemoryLimitInBytes: 150 * 1024 * 1024,
		}, nil)
	}

	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 150*1024*1024)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	checkActualMemoryLimit(t, container, uint64(150*1024*1024))

	t.Log("Update container memory limit after started")
	if goruntime.GOOS == "windows" {
		err = runtimeService.UpdateContainerResources(cn, nil, &runtime.WindowsContainerResources{
			MemoryLimitInBytes: 200 * 1024 * 1024,
		})
	} else {
		err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
			MemoryLimitInBytes: 200 * 1024 * 1024,
		}, nil)
	}
	require.NoError(t, err)

	t.Log("Check memory limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkMemoryLimit(t, spec, 200*1024*1024)

	checkActualMemoryLimit(t, container, uint64(200*1024*1024))
}

func TestUpdateContainerResources_CPULimit(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "update-container-resources")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 0.5 CPU for 60 seconds, but limit the container to only 10%.
	var resourceLimits ContainerOpts
	if goruntime.GOOS == "windows" {
		resourceLimits = WithWindowsResources(&runtime.WindowsContainerResources{
			// NOTE(claudiub): CpuMaximum is out of 10000, so 1000 means 10%.
			// CpuMaximum also refers to the percentage PER CPU, meaning that if we set it to 1000
			// and we have 2 CPUs, we would at most consume 10% of each CPU, totaling 20%.
			// So, we need to divide by the number of CPUs.
			CpuMaximum: int64(1000 / goruntime.NumCPU()),
		})
	} else {
		resourceLimits = WithResources(&runtime.LinuxContainerResources{
			CpuPeriod: 10000,
			CpuQuota:  1000,
		})
	}

	t.Log("Create a container with CPU limit")
	cnConfig := ContainerConfig(
		"container",
		testImage,
		resourceLimits,
		WithCommand("/consume-cpu/consume-cpu", "-millicores=500", "-duration-sec=60"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Check CPU limit in container OCI spec")
	container, err := containerdClient.LoadContainer(context.Background(), cn)
	require.NoError(t, err)
	spec, err := container.Spec(context.Background())
	require.NoError(t, err)
	checkCPULimit(t, spec, 1000)

	t.Log("Update container memory limit after created")
	if goruntime.GOOS == "windows" {
		err = runtimeService.UpdateContainerResources(cn, nil, &runtime.WindowsContainerResources{
			CpuMaximum: int64(1500 / goruntime.NumCPU()),
		})
	} else {
		err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
			CpuPeriod: 10000,
			CpuQuota:  1500,
		}, nil)
	}

	require.NoError(t, err)

	t.Log("Check CPU limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkCPULimit(t, spec, 1500)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	checkActualCPULimit(t, container, 0.15)

	t.Log("Update container CPU limit after started")
	if goruntime.GOOS == "windows" {
		err = runtimeService.UpdateContainerResources(cn, nil, &runtime.WindowsContainerResources{
			CpuMaximum: int64(2500 / goruntime.NumCPU()),
		})
	} else {
		err = runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
			CpuPeriod: 10000,
			CpuQuota:  2500,
		}, nil)
	}
	require.NoError(t, err)

	t.Log("Check CPU limit in container OCI spec")
	spec, err = container.Spec(context.Background())
	require.NoError(t, err)
	checkCPULimit(t, spec, 2500)

	checkActualCPULimit(t, container, 0.25)
}

func checkActualCPULimit(t *testing.T, container containerd.Container, expectedUsage float64) {
	t.Logf("Checking that the container consumes ~%f%% CPU", expectedUsage)
	minUsage := expectedUsage * 0.85
	maxUsage := expectedUsage * 1.15
	containerID := container.ID()

	var previous *runtime.ContainerStats
	statChecker := func() (bool, error) {
		current, err := runtimeService.ContainerStats(containerID)
		if err != nil {
			return false, err
		}

		if previous == nil {
			previous = current
			return false, nil
		}

		previousUsage := previous.GetCpu().GetUsageCoreNanoSeconds().GetValue()
		previousTimestamp := previous.GetCpu().GetTimestamp()
		currentUsage := current.GetCpu().GetUsageCoreNanoSeconds().GetValue()
		currentTimestamp := current.GetCpu().GetTimestamp()

		cpuUsage := float64(currentUsage-previousUsage) / float64(currentTimestamp-previousTimestamp)
		t.Logf("Container %s current CPU usage: %f%%.\n", containerID, cpuUsage*100)

		if cpuUsage >= minUsage && cpuUsage <= maxUsage {
			return true, nil
		}
		return false, nil
	}

	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

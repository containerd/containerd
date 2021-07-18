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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// Test to verify that the memory limit is enforced.
func TestContainerMemoryLimit(t *testing.T) {
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox-memory", "stats")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 150 MB memory for 60 seconds.
	var resourceLimits, command ContainerOpts
	if goruntime.GOOS == "windows" {
		// -d: Leak and touch memory in specified MBs
		// -c: Count of number of objects to allocate
		command = WithCommand("testlimit.exe", "-accepteula", "-d", "1", "-c", "150")
		resourceLimits = WithWindowsResources(&runtime.WindowsContainerResources{
			MemoryLimitInBytes: 100 * 1024 * 1024,
		})
	} else {
		// -m: spawn N workers spinning on malloc()/free()
		// --vm-bytes: malloc B bytes per vm worker (default is 256MB)
		// NOTE(claudiub): stress will error out if it's unable to allocate all the memory it needs.
		// So, we consume 95 MB and try to consume more afterwards.
		command = WithCommand("sh", "-c", "stress -m 95 --vm-bytes 1M --vm-hang 0 -t 60 & while true; do stress -m 55 --vm-bytes 1M --vm-hang 0 -t 60; done")
		resourceLimits = WithResources(&runtime.LinuxContainerResources{
			MemoryLimitInBytes: 100 * 1024 * 1024,
		})
	}

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		testImage,
		command,
		resourceLimits,
		WithTestLabels(),
		WithTestAnnotations(),
	)
	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	statChecker := memStatChecker(90, 101, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

// Test to verify that the memory limit update is applied.
func TestContainerUpdateMemoryLimit(t *testing.T) {
	if goruntime.GOOS == "windows" {
		// TODO(claudiub): remove this when UpdateContainerResources works on running Windows Containers.
		// https://github.com/containerd/containerd/issues/5187
		t.Skipf("Not supported on Windows.")
	}
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox-memory-limit", "stats")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 200 MB memory for 120 seconds.
	var resourceLimits, command ContainerOpts
	// -m: spawn N workers spinning on malloc()/free()
	// --vm-bytes: malloc B bytes per vm worker (default is 256MB)
	command = WithCommand("sh", "-c", "stress -m 95 --vm-bytes 1M --vm-hang 0 -t 60 & while true; do stress -m 105 --vm-bytes 1M --vm-hang 0 -t 10; done")
	resourceLimits = WithResources(&runtime.LinuxContainerResources{
		MemoryLimitInBytes: 100 * 1024 * 1024,
	})

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		testImage,
		command,
		resourceLimits,
		WithTestLabels(),
		WithTestAnnotations(),
	)
	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	t.Log("Checking that the container consumes ~100 MB memory")
	statChecker := memStatChecker(90, 101, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))

	t.Log("Updating container memory limit to 200 MB memory")
	require.NoError(t, runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		MemoryLimitInBytes: 200 * 1024 * 1024,
	}))

	t.Log("Checking that the container consumes ~200 MB memory")
	statChecker = memStatChecker(190, 201, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

// test to verify that the CPU limit is enforced.
func TestContainerCpuLimit(t *testing.T) {
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox-cpu", "stats")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 1 CPU for 60 seconds, but limit the container to only 15%.
	var resourceLimits ContainerOpts
	if goruntime.GOOS == "windows" {
		resourceLimits = WithWindowsResources(&runtime.WindowsContainerResources{
			// NOTE(claudiub): CpuMaximum is out of 10000, so 1500 means 15%.
			CpuMaximum: 1500,
		})
	} else {
		resourceLimits = WithResources(&runtime.LinuxContainerResources{
			CpuPeriod: 100000,
			CpuQuota:  15000,
		})
	}

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		testImage,
		resourceLimits,
		WithCommand("/consume-cpu/consume-cpu", "-millicores=1000", "-duration-sec=60"),
		WithTestLabels(),
		WithTestAnnotations(),
	)
	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	statChecker := cpuStatChecker(0.125, 0.175, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

// test to verify that the CPU limit update is applied.
func TestContainerUpdateCpuLimit(t *testing.T) {
	if goruntime.GOOS == "windows" {
		// TODO(claudiub): remove this when UpdateContainerResources works on running Windows Containers.
		// https://github.com/containerd/containerd/issues/5187
		t.Skipf("Not supported on Windows.")
	}
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox-cpu-limit", "stats")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	// consume 1 CPU for 60 seconds, but limit the container to only 15%.
	resourceLimits := WithResources(&runtime.LinuxContainerResources{
		CpuPeriod: 100000,
		CpuQuota:  15000,
	})

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		testImage,
		resourceLimits,
		WithCommand("/consume-cpu/consume-cpu", "-millicores=1000", "-duration-sec=60"),
		WithTestLabels(),
		WithTestAnnotations(),
	)
	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	t.Log("Checking that the container consumes ~15% CPU")
	statChecker := cpuStatChecker(0.125, 0.175, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))

	t.Log("Updating container CPU limit to 25%")
	require.NoError(t, runtimeService.UpdateContainerResources(cn, &runtime.LinuxContainerResources{
		CpuPeriod: 100000,
		CpuQuota:  25000,
	}))

	t.Log("Checking that the container consumes ~25%")
	statChecker = cpuStatChecker(0.225, 0.275, cn)
	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

func memStatChecker(minMb, maxMb uint64, containerID string) func() (bool, error) {
	return func() (bool, error) {
		s, err := runtimeService.ContainerStats(containerID)
		if err != nil {
			return false, err
		}

		consumedBytes := s.GetMemory().GetWorkingSetBytes().GetValue()
		fmt.Printf("Container %s current memory usage: %f MB.\n", containerID, float64(consumedBytes)/(1024*1024))

		if consumedBytes > minMb*1024*1024 && consumedBytes < maxMb*1024*1024 {
			return true, nil
		}
		return false, nil
	}
}

func cpuStatChecker(minUsage, maxUsage float64, containerID string) func() (bool, error) {
	var previous *runtime.ContainerStats
	return func() (bool, error) {
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
		fmt.Printf("Container %s current CPU usage: %f%%.\n", containerID, cpuUsage*100)

		if cpuUsage >= minUsage && cpuUsage <= maxUsage {
			return true, nil
		}
		return false, nil
	}
}

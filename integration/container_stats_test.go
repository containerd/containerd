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
	"errors"
	"fmt"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Test to verify for a container ID
func TestContainerStats(t *testing.T) {
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox1", "stats")

	EnsureImageExists(t, pauseImage)

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		pauseImage,
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

	t.Logf("Fetch stats for container")
	var s *runtime.ContainerStats
	require.NoError(t, Eventually(func() (bool, error) {
		s, err = runtimeService.ContainerStats(cn)
		if err != nil {
			return false, err
		}
		if s.GetWritableLayer().GetUsedBytes().GetValue() != 0 {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Logf("Verify stats received for container %q", cn)
	testStats(t, s, containerConfig)
}

// Test to verify if the consumed stats are correct.
func TestContainerConsumedStats(t *testing.T) {
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox1", "stats")

	testImage := GetImage(ResourceConsumer)
	EnsureImageExists(t, testImage)

	t.Logf("Create a container config and run container in a pod")
	containerConfig := ContainerConfig(
		"container1",
		testImage,
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

	t.Logf("Fetch initial stats for container")
	var s *runtime.ContainerStats
	require.NoError(t, Eventually(func() (bool, error) {
		s, err = runtimeService.ContainerStats(cn)
		if err != nil {
			return false, err
		}
		if s.GetMemory().GetWorkingSetBytes().GetValue() > 0 {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	initialMemory := s.GetMemory().GetWorkingSetBytes().GetValue()
	t.Logf("Initial container memory consumption is %f MB. Consume 100 MB and expect the reported stats to increase accordingly", float64(initialMemory)/(1024*1024))

	// consume 100 MB memory for 30 seconds.
	var command []string
	if goruntime.GOOS == "windows" {
		// -d: Leak and touch memory in specified MBs
		// -c: Count of number of objects to allocate
		command = []string{"testlimit.exe", "-accepteula", "-d", "25", "-c", "4"}
	} else {
		command = []string{"stress", "-m", "1", "--vm-bytes", "100M", "--vm-hang", "0", "-t", "30"}
	}

	go func() {
		_, _, err = runtimeService.ExecSync(cn, command, 30*time.Second)
	}()

	require.NoError(t, Eventually(func() (bool, error) {
		s, err = runtimeService.ContainerStats(cn)
		if err != nil {
			return false, err
		}
		if s.GetMemory().GetWorkingSetBytes().GetValue() > initialMemory+100*1024*1024 {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))
}

// Test to verify filtering without any filter
func TestContainerListStats(t *testing.T) {
	var (
		stats []*runtime.ContainerStats
		err   error
	)
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "running-pod", "statsls")

	EnsureImageExists(t, pauseImage)

	t.Logf("Create a container config and run containers in a pod")
	containerConfigMap := make(map[string]*runtime.ContainerConfig)
	for i := 0; i < 3; i++ {
		cName := fmt.Sprintf("container%d", i)
		containerConfig := ContainerConfig(
			cName,
			pauseImage,
			WithTestLabels(),
			WithTestAnnotations(),
		)
		cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
		require.NoError(t, err)
		containerConfigMap[cn] = containerConfig
		defer func() {
			assert.NoError(t, runtimeService.RemoveContainer(cn))
		}()
		require.NoError(t, runtimeService.StartContainer(cn))
		defer func() {
			assert.NoError(t, runtimeService.StopContainer(cn, 10))
		}()
	}

	t.Logf("Fetch all container stats")
	require.NoError(t, Eventually(func() (bool, error) {
		stats, err = runtimeService.ListContainerStats(&runtime.ContainerStatsFilter{})
		if err != nil {
			return false, err
		}
		for _, s := range stats {
			if s.GetWritableLayer().GetUsedBytes().GetValue() == 0 {
				return false, nil
			}
		}
		return true, nil
	}, time.Second, 30*time.Second))

	t.Logf("Verify all container stats")
	for _, s := range stats {
		testStats(t, s, containerConfigMap[s.GetAttributes().GetId()])
	}
}

// Test to verify filtering given a specific container ID
// TODO Convert the filter tests into table driven tests and unit tests
func TestContainerListStatsWithIdFilter(t *testing.T) {
	var (
		stats []*runtime.ContainerStats
		err   error
	)
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "running-pod", "statsls")

	EnsureImageExists(t, pauseImage)

	t.Logf("Create a container config and run containers in a pod")
	containerConfigMap := make(map[string]*runtime.ContainerConfig)
	for i := 0; i < 3; i++ {
		cName := fmt.Sprintf("container%d", i)
		containerConfig := ContainerConfig(
			cName,
			pauseImage,
			WithTestLabels(),
			WithTestAnnotations(),
		)
		cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
		containerConfigMap[cn] = containerConfig
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, runtimeService.RemoveContainer(cn))
		}()
		require.NoError(t, runtimeService.StartContainer(cn))
		defer func() {
			assert.NoError(t, runtimeService.StopContainer(cn, 10))
		}()
	}

	t.Logf("Fetch container stats for each container with Filter")
	for id := range containerConfigMap {
		require.NoError(t, Eventually(func() (bool, error) {
			stats, err = runtimeService.ListContainerStats(
				&runtime.ContainerStatsFilter{Id: id})
			if err != nil {
				return false, err
			}
			if len(stats) != 1 {
				return false, errors.New("unexpected stats length")
			}
			if stats[0].GetWritableLayer().GetUsedBytes().GetValue() != 0 {
				return true, nil
			}
			return false, nil
		}, time.Second, 30*time.Second))

		t.Logf("Verify container stats for %s", id)
		for _, s := range stats {
			require.Equal(t, s.GetAttributes().GetId(), id)
			testStats(t, s, containerConfigMap[id])
		}
	}
}

// Test to verify filtering given a specific Sandbox ID. Stats for
// all the containers in a pod should be returned
func TestContainerListStatsWithSandboxIdFilter(t *testing.T) {
	var (
		stats []*runtime.ContainerStats
		err   error
	)
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "running-pod", "statsls")

	EnsureImageExists(t, pauseImage)

	t.Logf("Create a container config and run containers in a pod")
	containerConfigMap := make(map[string]*runtime.ContainerConfig)
	for i := 0; i < 3; i++ {
		cName := fmt.Sprintf("container%d", i)
		containerConfig := ContainerConfig(
			cName,
			pauseImage,
			WithTestLabels(),
			WithTestAnnotations(),
		)
		cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
		containerConfigMap[cn] = containerConfig
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, runtimeService.RemoveContainer(cn))
		}()
		require.NoError(t, runtimeService.StartContainer(cn))
		defer func() {
			assert.NoError(t, runtimeService.StopContainer(cn, 10))
		}()
	}

	t.Logf("Fetch container stats for each container with Filter")
	require.NoError(t, Eventually(func() (bool, error) {
		stats, err = runtimeService.ListContainerStats(
			&runtime.ContainerStatsFilter{PodSandboxId: sb})
		if err != nil {
			return false, err
		}
		if len(stats) != 3 {
			return false, errors.New("unexpected stats length")
		}
		if stats[0].GetWritableLayer().GetUsedBytes().GetValue() != 0 {
			return true, nil
		}
		return false, nil
	}, time.Second, 45*time.Second))
	// TODO(claudiub): Reduce the timer above to 30 seconds once Windows flakiness has been addressed.
	t.Logf("Verify container stats for sandbox %q", sb)
	for _, s := range stats {
		testStats(t, s, containerConfigMap[s.GetAttributes().GetId()])
	}
}

// Test to verify filtering given a specific container ID and
// sandbox ID
func TestContainerListStatsWithIdSandboxIdFilter(t *testing.T) {
	var (
		stats []*runtime.ContainerStats
		err   error
	)
	t.Logf("Create a pod config and run sandbox container")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "running-pod", "statsls")

	EnsureImageExists(t, pauseImage)

	t.Logf("Create container config and run containers in a pod")
	containerConfigMap := make(map[string]*runtime.ContainerConfig)
	for i := 0; i < 3; i++ {
		cName := fmt.Sprintf("container%d", i)
		containerConfig := ContainerConfig(
			cName,
			pauseImage,
			WithTestLabels(),
			WithTestAnnotations(),
		)
		cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
		containerConfigMap[cn] = containerConfig
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, runtimeService.RemoveContainer(cn))
		}()
		require.NoError(t, runtimeService.StartContainer(cn))
		defer func() {
			assert.NoError(t, runtimeService.StopContainer(cn, 10))
		}()
	}
	t.Logf("Fetch container stats for sandbox ID and container ID filter")
	for id, config := range containerConfigMap {
		require.NoError(t, Eventually(func() (bool, error) {
			stats, err = runtimeService.ListContainerStats(
				&runtime.ContainerStatsFilter{Id: id, PodSandboxId: sb})
			if err != nil {
				return false, err
			}
			if len(stats) != 1 {
				return false, errors.New("unexpected stats length")
			}
			if stats[0].GetWritableLayer().GetUsedBytes().GetValue() != 0 {
				return true, nil
			}
			return false, nil
		}, time.Second, 30*time.Second))
		t.Logf("Verify container stats for sandbox %q and container %q filter", sb, id)
		for _, s := range stats {
			testStats(t, s, config)
		}
	}

	t.Logf("Fetch container stats for sandbox truncID and container truncID filter ")
	for id, config := range containerConfigMap {
		require.NoError(t, Eventually(func() (bool, error) {
			stats, err = runtimeService.ListContainerStats(
				&runtime.ContainerStatsFilter{Id: id[:3], PodSandboxId: sb[:3]})
			if err != nil {
				return false, err
			}
			if len(stats) != 1 {
				return false, errors.New("unexpected stats length")
			}
			if stats[0].GetWritableLayer().GetUsedBytes().GetValue() != 0 {
				return true, nil
			}
			return false, nil
		}, time.Second, 30*time.Second))
		t.Logf("Verify container stats for sandbox %q and container %q filter", sb, id)
		for _, s := range stats {
			testStats(t, s, config)
		}
	}
}

// TODO make this as options to use for dead container tests
func testStats(t *testing.T,
	s *runtime.ContainerStats,
	config *runtime.ContainerConfig,
) {
	require.NotEmpty(t, s.GetAttributes().GetId())
	require.NotEmpty(t, s.GetAttributes().GetMetadata())
	require.NotEmpty(t, s.GetAttributes().GetAnnotations())
	require.Equal(t, s.GetAttributes().GetLabels(), config.Labels)
	require.Equal(t, s.GetAttributes().GetAnnotations(), config.Annotations)
	require.Equal(t, s.GetAttributes().GetMetadata().Name, config.Metadata.Name)
	require.NotEmpty(t, s.GetAttributes().GetLabels())
	require.NotEmpty(t, s.GetCpu().GetTimestamp())
	require.NotEmpty(t, s.GetCpu().GetUsageCoreNanoSeconds().GetValue())
	require.NotEmpty(t, s.GetMemory().GetTimestamp())
	require.NotEmpty(t, s.GetMemory().GetWorkingSetBytes().GetValue())
	require.NotEmpty(t, s.GetWritableLayer().GetTimestamp())
	require.NotEmpty(t, s.GetWritableLayer().GetFsId().GetMountpoint())
	require.NotEmpty(t, s.GetWritableLayer().GetUsedBytes().GetValue())

	// Windows does not collect inodes stats.
	if goruntime.GOOS != "windows" {
		require.NotEmpty(t, s.GetWritableLayer().GetInodesUsed().GetValue())
	}
}

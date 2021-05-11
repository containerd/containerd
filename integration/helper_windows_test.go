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
	"time"

	"github.com/containerd/containerd"
	"github.com/stretchr/testify/require"
)

func checkActualMemoryLimit(t *testing.T, container containerd.Container, expectedBytes uint64) {
	t.Logf("Checking that the container consumes ~%d MB memory", expectedBytes/(1024*1024))
	minBytes := uint64(float64(expectedBytes) * 0.85)
	maxBytes := uint64(float64(expectedBytes) * 1.15)
	containerID := container.ID()

	statChecker := func() (bool, error) {
		s, err := runtimeService.ContainerStats(containerID)
		if err != nil {
			return false, err
		}

		consumedBytes := s.GetMemory().GetWorkingSetBytes().GetValue()
		t.Logf("Container %s current memory usage: %f MB.\n", containerID, float64(consumedBytes)/(1024*1024))

		if consumedBytes > minBytes && consumedBytes < maxBytes {
			return true, nil
		}
		return false, nil
	}

	require.NoError(t, Eventually(statChecker, 2*time.Second, 20*time.Second))
	require.NoError(t, Consistently(statChecker, 2*time.Second, 10*time.Second))
}

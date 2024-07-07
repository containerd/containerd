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

package server

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/containerd/containerd/api/types"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/stretchr/testify/assert"
)

func TestGetUsageNanoCores(t *testing.T) {
	timestamp := time.Now()
	secondAfterTimeStamp := timestamp.Add(time.Second)
	ID := "ID"

	for _, test := range []struct {
		desc                        string
		firstCPUValue               uint64
		secondCPUValue              uint64
		expectedNanoCoreUsageFirst  uint64
		expectedNanoCoreUsageSecond uint64
	}{
		{
			desc:                        "stat",
			firstCPUValue:               50,
			secondCPUValue:              500,
			expectedNanoCoreUsageFirst:  0,
			expectedNanoCoreUsageSecond: 450,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: ID},
			)
			assert.NoError(t, err)

			// calculate for first iteration
			// first run so container stats will be nil
			assert.Nil(t, container.Stats)
			nanoCoreUsage := calculateNanoCores(test.firstCPUValue, container.Stats, timestamp)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedNanoCoreUsageFirst, nanoCoreUsage)

			// fill in the stats as if they now exist
			container.Stats = &stats.ContainerStats{}
			container.Stats.UsageCoreNanoSeconds = test.firstCPUValue
			container.Stats.Timestamp = timestamp
			assert.NotNil(t, container.Stats)

			// calculate for second iteration
			nanoCoreUsage = calculateNanoCores(test.secondCPUValue, container.Stats, secondAfterTimeStamp)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedNanoCoreUsageSecond, nanoCoreUsage)
		})
	}

}

func TestBuildMetricsRequest(t *testing.T) {
	testCases := []struct {
		desc               string
		sandboxIDs         []string
		containerIDs       []string
		expectedFilters    []string
		expectedSandboxes  map[string]struct{}
		expectedContainers map[string]struct{}
	}{
		{
			desc:               "single sandbox and container",
			sandboxIDs:         []string{"sandbox1"},
			containerIDs:       []string{"container1"},
			expectedFilters:    []string{"id==sandbox1", "id==container1"},
			expectedSandboxes:  map[string]struct{}{"sandbox1": {}},
			expectedContainers: map[string]struct{}{"container1": {}},
		},
		{
			desc:               "multiple sandboxes and containers",
			sandboxIDs:         []string{"sandbox1", "sandbox2"},
			containerIDs:       []string{"container1", "container2"},
			expectedFilters:    []string{"id==sandbox1", "id==sandbox2", "id==container1", "id==container2"},
			expectedSandboxes:  map[string]struct{}{"sandbox1": {}, "sandbox2": {}},
			expectedContainers: map[string]struct{}{"container1": {}, "container2": {}},
		},
		{
			desc:               "no sandboxes or containers",
			sandboxIDs:         []string{},
			containerIDs:       []string{},
			expectedFilters:    []string{},
			expectedSandboxes:  map[string]struct{}{},
			expectedContainers: map[string]struct{}{},
		},
		{
			desc:               "single sandbox, no containers",
			sandboxIDs:         []string{"sandbox1"},
			containerIDs:       []string{},
			expectedFilters:    []string{"id==sandbox1"},
			expectedSandboxes:  map[string]struct{}{"sandbox1": {}},
			expectedContainers: map[string]struct{}{},
		},
		{
			desc:               "no sandboxes, single container",
			sandboxIDs:         []string{},
			containerIDs:       []string{"container1"},
			expectedFilters:    []string{"id==container1"},
			expectedSandboxes:  map[string]struct{}{},
			expectedContainers: map[string]struct{}{"container1": {}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			c := newTestCRIService()
			mm := newMetricMonitor(c)

			// Add sandboxes and containers to the stores
			for _, id := range tc.sandboxIDs {
				c.sandboxStore.Add(sandboxstore.Sandbox{
					Metadata: sandboxstore.Metadata{ID: id},
				})
			}
			for _, id := range tc.containerIDs {
				c.containerStore.Add(containerstore.Container{
					Metadata: containerstore.Metadata{ID: id},
				})
			}

			req, sandboxes, containers := mm.buildMetricsRequest()

			assert.NotNil(t, req)
			assert.NotNil(t, sandboxes)
			assert.NotNil(t, containers)
			for _, filter := range tc.expectedFilters {
				assert.Contains(t, req.Filters, filter)
			}
			for sandboxID := range tc.expectedSandboxes {
				assert.Contains(t, sandboxes, sandboxID)
			}
			for containerID := range tc.expectedContainers {
				assert.Contains(t, containers, containerID)
			}
		})
	}
}

func TestCollectNoSandboxesOrContainers(t *testing.T) {
	c := newTestCRIService()
	mm := newMetricMonitor(c)

	// No sandboxes or containers are added to the stores
	err := mm.collect()
	assert.Nil(t, err)
}

func TestProcessMetric(t *testing.T) {
	testCases := []struct {
		desc                         string
		stat                         *types.Metric
		sandboxes                    map[string]struct{}
		containers                   map[string]struct{}
		expectErr                    bool
		expectedUsageCoreNanoSeconds uint64
	}{
		{
			desc:       "nil stat",
			stat:       nil,
			sandboxes:  map[string]struct{}{},
			containers: map[string]struct{}{},
			expectErr:  true,
		},
		{
			desc: "Unknown metric type",
			stat: &types.Metric{
				Data: toProto(&types.Sandbox{})},
			sandboxes: map[string]struct{}{"sandbox1": {}},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			runProcessMetricTest(tc, t)
		})
	}
}

func runProcessMetricTest(tc struct {
	desc                         string
	stat                         *types.Metric
	sandboxes                    map[string]struct{}
	containers                   map[string]struct{}
	expectErr                    bool
	expectedUsageCoreNanoSeconds uint64
}, t *testing.T) {
	c := newTestCRIService()
	mm := newMetricMonitor(c)

	for sandboxID := range tc.sandboxes {
		c.sandboxStore.Add(sandboxstore.Sandbox{
			Metadata: sandboxstore.Metadata{ID: sandboxID},
		})
	}
	for containerID := range tc.containers {
		c.containerStore.Add(containerstore.Container{
			Metadata: containerstore.Metadata{ID: containerID},
		})
	}

	err := mm.processMetric(tc.stat, tc.sandboxes)
	if tc.expectErr {
		assert.Error(t, err)
		return
	}
	assert.NoError(t, err)

	var stat *stats.ContainerStats
	if strings.HasPrefix(tc.stat.ID, "sandbox") {
		cntr, err := c.sandboxStore.Get(tc.stat.ID)
		assert.NoError(t, err)
		stat = cntr.Stats
	} else {
		cntr, err := c.containerStore.Get(tc.stat.ID)
		assert.NoError(t, err)
		stat = cntr.Stats
	}
	assert.NotNil(t, stat)
	assert.Equal(t, tc.expectedUsageCoreNanoSeconds, stat.UsageCoreNanoSeconds)
}

func toProto(metric any) *anypb.Any {
	data, err := protobuf.MarshalAnyToProto(metric)
	if err != nil {
		panic("failed to marshal proto: " + err.Error())
	}
	return data
}

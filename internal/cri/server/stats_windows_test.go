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
	"testing"
	"time"

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	"github.com/containerd/containerd/v2/api/types"
	"github.com/containerd/containerd/v2/protobuf"
)

func TestProcessMetricWindows(t *testing.T) {
	testCases := []struct {
		desc                         string
		stat                         *types.Metric
		sandboxes                    map[string]struct{}
		containers                   map[string]struct{}
		expectErr                    bool
		expectedUsageCoreNanoSeconds uint64
	}{
		{
			desc: "wstats.Statistics with valid metric for container",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&wstats.Statistics{
					Container: &wstats.Statistics_Windows{
						Windows: &wstats.WindowsContainerStatistics{
							Timestamp: protobuf.ToTimestamp(time.Now()),
							Processor: &wstats.WindowsContainerProcessorStatistics{
								TotalRuntimeNS: 100,
							},
						},
					},
				})},
			sandboxes:                    map[string]struct{}{"sandbox1": {}},
			containers:                   map[string]struct{}{"container1": {}},
			expectErr:                    false,
			expectedUsageCoreNanoSeconds: 100,
		},
		{
			desc: "wstats.Statistics with valid metric for sandbox",
			stat: &types.Metric{
				ID: "sandbox1",
				Data: toProto(&wstats.Statistics{
					Container: &wstats.Statistics_Windows{
						Windows: &wstats.WindowsContainerStatistics{
							Timestamp: protobuf.ToTimestamp(time.Now()),
							Processor: &wstats.WindowsContainerProcessorStatistics{
								TotalRuntimeNS: 100,
							},
						},
					},
				})},
			sandboxes:                    map[string]struct{}{"sandbox1": {}},
			containers:                   map[string]struct{}{"container1": {}},
			expectErr:                    false,
			expectedUsageCoreNanoSeconds: 100,
		},
		{
			desc: "wstats.Statistics with nil should save as zero (hostprocess container scenario)",
			stat: &types.Metric{
				ID: "sandbox1",
				Data: toProto(&wstats.Statistics{
					Container: nil,
				}),
			},
			sandboxes:                    map[string]struct{}{"sandbox1": {}},
			containers:                   map[string]struct{}{"container1": {}},
			expectErr:                    false,
			expectedUsageCoreNanoSeconds: 0,
		},
		{
			desc: "wstats.Statistics with invalid metric should fail",
			stat: &types.Metric{
				ID: "sandbox1",
				Data: toProto(&wstats.Statistics{
					Container: &wstats.Statistics_Windows{
						Windows: nil,
					},
				})},
			sandboxes:  map[string]struct{}{"sandbox1": {}},
			containers: map[string]struct{}{"container1": {}},
			expectErr:  true,
		},
		{
			desc: "can't find container in store",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&wstats.Statistics{
					Container: &wstats.Statistics_Windows{
						Windows: &wstats.WindowsContainerStatistics{
							Timestamp: protobuf.ToTimestamp(time.Now()),
							Processor: &wstats.WindowsContainerProcessorStatistics{
								TotalRuntimeNS: 100,
							},
						},
					},
				})},
			sandboxes:  map[string]struct{}{"sandbox1": {}},
			containers: map[string]struct{}{},
			expectErr:  true,
		},
		{
			desc: "can't find container in store or sandbox",
			stat: &types.Metric{
				ID: "sandbox2",
				Data: toProto(&wstats.Statistics{
					Container: &wstats.Statistics_Windows{
						Windows: &wstats.WindowsContainerStatistics{
							Timestamp: protobuf.ToTimestamp(time.Now()),
							Processor: &wstats.WindowsContainerProcessorStatistics{
								TotalRuntimeNS: 100,
							},
						},
					},
				})},
			sandboxes:  map[string]struct{}{"sandbox1": {}},
			containers: map[string]struct{}{"container1": {}},
			expectErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			runProcessMetricTest(tc, t)
		})
	}
}

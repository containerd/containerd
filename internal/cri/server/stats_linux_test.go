package server

import (
	"github.com/containerd/containerd/v2/api/types"
	"testing"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
)

func TestProcessMetricLinux(t *testing.T) {
	testCases := []struct {
		desc                         string
		stat                         *types.Metric
		sandboxes                    map[string]struct{}
		containers                   map[string]struct{}
		expectErr                    bool
		expectedUsageCoreNanoSeconds uint64
	}{
		{
			desc: "cg1.Metrics with valid metric for container",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&cg1.Metrics{
					CPU: &cg1.CPUStat{
						Usage: &cg1.CPUUsage{
							Total: 1000,
						},
					},
				})},
			sandboxes:                    map[string]struct{}{"sandbox1": {}},
			containers:                   map[string]struct{}{"container1": {}},
			expectErr:                    false,
			expectedUsageCoreNanoSeconds: 1000,
		},
		{
			desc: "cg1.Metrics with invalid metric should fail",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&cg1.Metrics{
					CPU: &cg1.CPUStat{
						Usage: nil,
					},
				})},
			sandboxes:  map[string]struct{}{"sandbox1": {}},
			containers: map[string]struct{}{"container1": {}},
			expectErr:  true,
		},
		{
			desc: "cg2.Metrics with valid metric for container",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&cg2.Metrics{
					CPU: &cg2.CPUStat{
						UsageUsec: 1000,
					},
				})},
			sandboxes:                    map[string]struct{}{"sandbox1": {}},
			containers:                   map[string]struct{}{"container1": {}},
			expectErr:                    false,
			expectedUsageCoreNanoSeconds: 1000000,
		},
		{
			desc: "cg2.Metrics with invalid metric should fail",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&cg2.Metrics{
					CPU: nil,
				})},
			sandboxes:  map[string]struct{}{"sandbox1": {}},
			containers: map[string]struct{}{"container1": {}},
			expectErr:  true,
		},
		{
			desc: "can't find container in store",
			stat: &types.Metric{
				ID: "container1",
				Data: toProto(&cg1.Metrics{
					CPU: &cg1.CPUStat{
						Usage: &cg1.CPUUsage{
							Total: 1000,
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
				Data: toProto(&cg1.Metrics{
					CPU: &cg1.CPUStat{
						Usage: &cg1.CPUUsage{
							Total: 1000,
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

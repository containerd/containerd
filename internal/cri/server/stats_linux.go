package server

import (
	"errors"
	"fmt"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/v2/api/types"
)

func (m *metricMonitor) extractStats(sandboxes map[string]struct{}, stat *types.Metric) (any, error) {
	// in the case of Linux we override the result from the stat call with a call to cgroup directly
	// This might not be needed once internal/cri/server/podsandbox/sandbox_stats.go is implemented
	if _, ok := sandboxes[stat.ID]; ok {
		sandbox, err := m.c.sandboxStore.Get(stat.ID)
		if err != nil {
			return nil, err
		}
		sbmetric, err := metricsForSandbox(sandbox)
		if err != nil {
			return nil, err
		}
		return sbmetric, nil
	}

	return convertMetric(stat)
}

func convertMetric(stats *types.Metric) (any, error) {
	var data interface{}
	switch {
	case typeurl.Is(stats.Data, (*cg1.Metrics)(nil)):
		data = &cg1.Metrics{}
	case typeurl.Is(stats.Data, (*cg2.Metrics)(nil)):
		data = &cg2.Metrics{}
	default:
		return nil, errors.New("cannot convert metric data to cgroups.Metrics")
	}

	if err := typeurl.UnmarshalTo(stats.Data, data); err != nil {
		return nil, fmt.Errorf("failed to extract container stat: %w", err)
	}

	return data, nil
}

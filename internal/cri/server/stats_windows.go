package server

import (
	"fmt"

	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/v2/api/types"
)

func (m *metricMonitor) extractStats(sandboxes map[string]struct{}, stat *types.Metric) (any, error) {
	return convertMetric(stat)
}

func convertMetric(stats *types.Metric) (any, error) {
	containerStatsData, err := typeurl.UnmarshalAny(stats.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to extract stat for container with id %s: %w", stats.ID, err)
	}

	return containerStatsData, nil
}

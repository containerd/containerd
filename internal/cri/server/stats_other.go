//go:build !windows && !linux

package server

import (
	"fmt"

	"github.com/containerd/containerd/v2/api/types"
)

func (m *metricMonitor) extractStats(sandboxes map[string]struct{}, stat *types.Metric) (any, error) {
	return nil, fmt.Errorf("stat is not support on this platform")
}

func convertMetric(stats *types.Metric) (any, error) {
	return nil, fmt.Errorf("stat is not support on this platform")
}

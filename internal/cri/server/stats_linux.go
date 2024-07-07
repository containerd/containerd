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
	"errors"
	"fmt"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/api/types"
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

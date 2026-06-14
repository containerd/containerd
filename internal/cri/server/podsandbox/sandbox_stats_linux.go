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

package podsandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/pkg/protobuf"
)

// Metrics returns the parent cgroup stats for the pod sandbox, encoded as a
// *types.Metric (cgroupv1: *cgroup1/stats.Metrics, cgroupv2: *cgroup2/stats.Metrics).
func (c *Controller) Metrics(ctx context.Context, sandboxID string) (*types.Metric, error) {
	sb := c.store.Get(sandboxID)
	if sb == nil {
		return nil, fmt.Errorf("sandbox %q not found: %w", sandboxID, errdefs.ErrNotFound)
	}

	cgroupPath := sb.Metadata.Config.GetLinux().GetCgroupParent()
	if cgroupPath == "" {
		return nil, fmt.Errorf("sandbox %q has no cgroup parent", sandboxID)
	}

	var data any
	switch cgroups.Mode() {
	case cgroups.Unified:
		cg, err := cgroupsv2.Load(cgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup %q: %w", cgroupPath, err)
		}
		stats, err := cg.StatFiltered(cgroupsv2.StatCPU | cgroupsv2.StatMemory)
		if err != nil {
			return nil, fmt.Errorf("failed to read sandbox cgroup %q: %w", cgroupPath, err)
		}
		data = stats
	default:
		control, err := cgroup1.Load(cgroup1.StaticPath(cgroupPath))
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup %q: %w", cgroupPath, err)
		}
		stats, err := control.Stat(cgroup1.IgnoreNotExist)
		if err != nil {
			return nil, fmt.Errorf("failed to read sandbox cgroup %q: %w", cgroupPath, err)
		}
		data = stats
	}

	pbAny, err := typeurl.MarshalAnyToProto(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sandbox metrics: %w", err)
	}

	return &types.Metric{
		Timestamp: protobuf.ToTimestamp(time.Now()),
		ID:        sandboxID,
		Data:      pbAny,
	}, nil
}

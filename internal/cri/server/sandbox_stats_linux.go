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
	"context"
	"fmt"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func (c *criService) podSandboxStats(
	ctx context.Context,
	sandbox sandboxstore.Sandbox) (*runtime.PodSandboxStats, error) {
	meta := sandbox.Metadata

	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, fmt.Errorf("failed to get pod sandbox stats since sandbox container %q is not in ready state: %w", meta.ID, errdefs.ErrUnavailable)
	}

	stats, err := cgroupMetricsForSandbox(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed getting metrics for sandbox %s: %w", sandbox.ID, err)
	}

	podSandboxStats := &runtime.PodSandboxStats{
		Linux: &runtime.LinuxPodSandboxStats{},
		Attributes: &runtime.PodSandboxAttributes{
			Id:          meta.ID,
			Metadata:    meta.Config.GetMetadata(),
			Labels:      meta.Config.GetLabels(),
			Annotations: meta.Config.GetAnnotations(),
		},
	}

	timestamp := time.Now()

	cpuStats, err := c.cpuContainerStats(meta.ID, true /* isSandbox */, *stats, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain cpu stats: %w", err)
	}
	if cpuStats != nil && cpuStats.UsageCoreNanoSeconds != nil {
		nanoUsage, err := c.getUsageNanoCores(meta.ID, true /* isSandbox */, cpuStats.UsageCoreNanoSeconds.Value, timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to get usage nano cores: %w", err)
		}
		cpuStats.UsageNanoCores = &runtime.UInt64Value{Value: nanoUsage}
	}
	podSandboxStats.Linux.Cpu = cpuStats

	memoryStats, err := c.memoryContainerStats(meta.ID, *stats, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain memory stats: %w", err)
	}
	podSandboxStats.Linux.Memory = memoryStats

	if sandbox.NetNSPath != "" {
		linkStats, err := getContainerNetIO(ctx, sandbox.NetNSPath)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain network stats: %w", err)
		}
		podSandboxStats.Linux.Network = &runtime.NetworkUsage{
			Timestamp: timestamp.UnixNano(),
			DefaultInterface: &runtime.NetworkInterfaceUsage{
				Name:     defaultIfName,
				RxBytes:  &runtime.UInt64Value{Value: linkStats.RxBytes},
				RxErrors: &runtime.UInt64Value{Value: linkStats.RxErrors},
				TxBytes:  &runtime.UInt64Value{Value: linkStats.TxBytes},
				TxErrors: &runtime.UInt64Value{Value: linkStats.TxErrors},
			},
		}
	}

	listContainerStatsRequest := &runtime.ListContainerStatsRequest{Filter: &runtime.ContainerStatsFilter{PodSandboxId: meta.ID}}
	css, err := c.listContainerStats(ctx, listContainerStatsRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain container stats during podSandboxStats call: %w", err)
	}
	var pidCount uint64
	for _, cs := range css {
		pidCount += cs.pids
		podSandboxStats.Linux.Containers = append(podSandboxStats.Linux.Containers, cs.stats)
	}
	podSandboxStats.Linux.Process = &runtime.ProcessUsage{
		Timestamp:    timestamp.UnixNano(),
		ProcessCount: &runtime.UInt64Value{Value: pidCount},
	}

	return podSandboxStats, nil
}

// https://github.com/cri-o/cri-o/blob/74a5cf8dffd305b311eb1c7f43a4781738c388c1/internal/oci/stats.go#L32
func getContainerNetIO(ctx context.Context, netNsPath string) (netlink.LinkStatistics64, error) {
	var stats netlink.LinkStatistics64
	err := ns.WithNetNSPath(netNsPath, func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(defaultIfName)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("unable to retrieve network namespace stats for netNsPath: %v, interface: %v", netNsPath, defaultIfName)
			return err
		}
		attrs := link.Attrs()
		if attrs != nil && attrs.Statistics != nil {
			stats = netlink.LinkStatistics64(*attrs.Statistics)
		}
		return nil
	})

	return stats, err
}

func cgroupMetricsForSandbox(sandbox sandboxstore.Sandbox) (*cgroupMetrics, error) {
	cgroupPath := sandbox.Config.GetLinux().GetCgroupParent()
	if cgroupPath == "" {
		return nil, fmt.Errorf("failed to get cgroup metrics for sandbox %v because cgroupPath is empty", sandbox.ID)
	}

	switch cgroups.Mode() {
	case cgroups.Unified:
		cg, err := cgroupsv2.Load(cgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup: %v: %w", cgroupPath, err)
		}
		stats, err := cg.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup: %v: %w", cgroupPath, err)
		}
		return &cgroupMetrics{v2: stats}, nil
	default:
		control, err := cgroup1.Load(cgroup1.StaticPath(cgroupPath))
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup %v: %w", cgroupPath, err)
		}
		stats, err := control.Stat(cgroup1.IgnoreNotExist)
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup %v: %w", cgroupPath, err)
		}
		return &cgroupMetrics{v1: stats}, nil
	}
}

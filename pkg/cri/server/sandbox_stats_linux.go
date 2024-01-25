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
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) podSandboxStats(
	ctx context.Context,
	sandbox sandboxstore.Sandbox) (*runtime.PodSandboxStats, error) {
	meta := sandbox.Metadata

	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, fmt.Errorf("failed to get pod sandbox stats since sandbox container %q is not in ready state: %w", meta.ID, errdefs.ErrUnavailable)
	}

	stats, err := metricsForSandbox(sandbox)
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

	if stats != nil {
		timestamp := time.Now()

		cpuStats, err := c.cpuContainerStats(meta.ID, true /* isSandbox */, stats, timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain cpu stats: %w", err)
		}
		podSandboxStats.Linux.Cpu = cpuStats

		memoryStats, err := c.memoryContainerStats(meta.ID, stats, timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain memory stats: %w", err)
		}
		podSandboxStats.Linux.Memory = memoryStats

		if sandbox.NetNSPath != "" {
			rxBytes, rxErrors, txBytes, txErrors := getContainerNetIO(ctx, sandbox.NetNSPath)
			podSandboxStats.Linux.Network = &runtime.NetworkUsage{
				DefaultInterface: &runtime.NetworkInterfaceUsage{
					Name:     defaultIfName,
					RxBytes:  &runtime.UInt64Value{Value: rxBytes},
					RxErrors: &runtime.UInt64Value{Value: rxErrors},
					TxBytes:  &runtime.UInt64Value{Value: txBytes},
					TxErrors: &runtime.UInt64Value{Value: txErrors},
				},
			}
		}

		var pidCount uint64
		for _, cntr := range c.containerStore.List() {
			if cntr.SandboxID != sandbox.ID {
				continue
			}

			state := cntr.Status.Get().State()
			if state != runtime.ContainerState_CONTAINER_RUNNING {
				continue
			}

			task, err := cntr.Container.Task(ctx, nil)
			if err != nil {
				return nil, err
			}

			processes, err := task.Pids(ctx)
			if err != nil {
				return nil, err
			}
			pidCount += uint64(len(processes))

		}
		podSandboxStats.Linux.Process = &runtime.ProcessUsage{
			Timestamp:    timestamp.UnixNano(),
			ProcessCount: &runtime.UInt64Value{Value: pidCount},
		}

		listContainerStatsRequest := &runtime.ListContainerStatsRequest{Filter: &runtime.ContainerStatsFilter{PodSandboxId: meta.ID}}
		resp, err := c.ListContainerStats(ctx, listContainerStatsRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain container stats during podSandboxStats call: %w", err)
		}
		podSandboxStats.Linux.Containers = resp.GetStats()
	}

	return podSandboxStats, nil
}

// https://github.com/cri-o/cri-o/blob/74a5cf8dffd305b311eb1c7f43a4781738c388c1/internal/oci/stats.go#L32
func getContainerNetIO(ctx context.Context, netNsPath string) (rxBytes, rxErrors, txBytes, txErrors uint64) {
	ns.WithNetNSPath(netNsPath, func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(defaultIfName)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("unable to retrieve network namespace stats for netNsPath: %v, interface: %v", netNsPath, defaultIfName)
			return err
		}
		attrs := link.Attrs()
		if attrs != nil && attrs.Statistics != nil {
			rxBytes = attrs.Statistics.RxBytes
			rxErrors = attrs.Statistics.RxErrors
			txBytes = attrs.Statistics.TxBytes
			txErrors = attrs.Statistics.TxErrors
		}
		return nil
	})

	return rxBytes, rxErrors, txBytes, txErrors
}

func metricsForSandbox(sandbox sandboxstore.Sandbox) (interface{}, error) {
	cgroupPath := sandbox.Config.GetLinux().GetCgroupParent()

	if cgroupPath == "" {
		return nil, fmt.Errorf("failed to get cgroup metrics for sandbox %v because cgroupPath is empty", sandbox.ID)
	}

	var statsx interface{}
	if cgroups.Mode() == cgroups.Unified {
		cg, err := cgroupsv2.Load(cgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup: %v: %w", cgroupPath, err)
		}
		stats, err := cg.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup: %v: %w", cgroupPath, err)
		}
		statsx = stats

	} else {
		control, err := cgroup1.Load(cgroup1.StaticPath(cgroupPath))
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup %v: %w", cgroupPath, err)
		}
		stats, err := control.Stat(cgroup1.IgnoreNotExist)
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup %v: %w", cgroupPath, err)
		}
		statsx = stats
	}

	return statsx, nil
}

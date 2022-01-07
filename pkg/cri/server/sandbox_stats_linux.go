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
	"fmt"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/cgroups"
	cgroupsv2 "github.com/containerd/cgroups/v2"

	"github.com/vishvananda/netlink"

	"github.com/containerd/containerd/log"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
)

func (c *criService) podSandboxStats(
	ctx context.Context,
	sandbox sandboxstore.Sandbox,
	stats interface{},
) (*runtime.PodSandboxStats, error) {
	meta := sandbox.Metadata

	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, fmt.Errorf("failed to get pod sandbox stats since sandbox container %q is not in ready state", meta.ID)
	}

	var podSandboxStats runtime.PodSandboxStats
	podSandboxStats.Attributes = &runtime.PodSandboxAttributes{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}

	podSandboxStats.Linux = &runtime.LinuxPodSandboxStats{}

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

	return &podSandboxStats, nil
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
		cg, err := cgroupsv2.LoadManager("/sys/fs/cgroup", cgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup: %v: %w", cgroupPath, err)
		}
		stats, err := cg.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup: %v: %w", cgroupPath, err)
		}
		statsx = stats

	} else {
		control, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(cgroupPath))
		if err != nil {
			return nil, fmt.Errorf("failed to load sandbox cgroup %v: %w", cgroupPath, err)
		}
		stats, err := control.Stat(cgroups.IgnoreNotExist)
		if err != nil {
			return nil, fmt.Errorf("failed to get stats for cgroup %v: %w", cgroupPath, err)
		}
		statsx = stats
	}

	return statsx, nil
}

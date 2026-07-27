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

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) ListMetricDescriptors(context.Context, *runtime.ListMetricDescriptorsRequest) (*runtime.ListMetricDescriptorsResponse, error) {
	descriptors := c.getMetricDescriptors()

	metricDescriptors := make([]*runtime.MetricDescriptor, 0)
	for _, descriptor := range descriptors {
		metricDescriptors = append(metricDescriptors, descriptor...)
	}

	return &runtime.ListMetricDescriptorsResponse{Descriptors: metricDescriptors}, nil
}

func (c *criService) getMetricDescriptors() map[string][]*runtime.MetricDescriptor {
	descriptors := map[string][]*runtime.MetricDescriptor{
		CPUUsageMetrics: {
			containerCPUUserSecondsTotal,
			containerCPUSystemSecondsTotal,
			containerCPUUsageSecondsTotal,
			containerCPUCfsPeriodsTotal,
			containerCPUCfsThrottledPeriodsTotal,
			containerCPUCfsThrottledSecondsTotal,
		},
		MemoryUsageMetrics: {
			containerMemoryCache,
			containerMemoryRss,
			containerMemoryKernelUsage,
			containerMemoryMappedFile,
			containerMemorySwap,
			containerMemoryFailcnt,
			containerMemoryUsageBytes,
			containerMemoryMaxUsageBytes,
			containerMemoryWorkingSetBytes,
			containerMemoryTotalActiveFileBytes,
			containerMemoryTotalInactiveFileBytes,
			containerMemoryFailuresTotal,
			containerOomEventsTotal,
		},
		NetworkUsageMetrics: {
			containerNetworkReceiveBytesTotal,
			containerNetworkReceivePacketsTotal,
			containerNetworkReceivePacketsDroppedTotal,
			containerNetworkReceiveErrorsTotal,
			containerNetworkTransmitBytesTotal,
			containerNetworkTransmitPacketsTotal,
			containerNetworkTransmitPacketsDroppedTotal,
			containerNetworkTransmitErrorsTotal,
		},
		DiskUsageMetrics: {
			containerFsInodesFree,
			containerFsInodesTotal,
			containerFsLimitBytes,
			containerFsUsageBytes,
		},
		DiskIOMetrics: {
			containerFsReadsBytesTotal,
			containerFsReadsTotal,
			containerFsWritesBytesTotal,
			containerFsWritesTotal,
			containerBlkioDeviceUsageTotal,
		},
		ProcessMetrics: {
			containerProcesses,
			containerFileDescriptors,
			containerSockets,
			containerThreadsMax,
			containerThreads,
			containerUlimitsSoft,
		},
		MiscellaneousMetrics: {
			containerLastSeen,
			containerStartTimeSeconds,
		},
		ContainerSpecMetrics: {
			containerSpecCPUPeriod,
			containerSpecCPUShares,
			containerSpecMemoryLimitBytes,
			containerSpecMemoryReservationLimitBytes,
			containerSpecMemorySwapLimitBytes,
		},
	}
	return descriptors
}

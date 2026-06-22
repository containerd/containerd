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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	CPUUsageMetrics      = "cpu"
	MemoryUsageMetrics   = "memory"
	NetworkUsageMetrics  = "network"
	DiskIOMetrics        = "diskIO"
	DiskUsageMetrics     = "disk"
	ProcessMetrics       = "process"
	MiscellaneousMetrics = "misc"
	ContainerSpecMetrics = "container_spec"
)

var (
	baseLabelKeys    = []string{"pod", "namespace", "container", "id", "image"}
	networkLabelKeys = append(baseLabelKeys, "interface")
	diskLabelKeys    = append(baseLabelKeys, "device")
)

// CPU metrics
var (
	containerCPUUserSecondsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_user_seconds_total",
		Help:      "Cumulative user CPU time consumed in seconds.",
		LabelKeys: baseLabelKeys,
	}
	containerCPUSystemSecondsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_system_seconds_total",
		Help:      "Cumulative system CPU time consumed in seconds.",
		LabelKeys: baseLabelKeys,
	}
	containerCPUUsageSecondsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_usage_seconds_total",
		Help:      "Cumulative CPU time consumed in seconds.",
		LabelKeys: baseLabelKeys,
	}
	containerCPUCfsPeriodsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_cfs_periods_total",
		Help:      "Number of elapsed enforcement period intervals.",
		LabelKeys: baseLabelKeys,
	}
	containerCPUCfsThrottledPeriodsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_cfs_throttled_periods_total",
		Help:      "Number of CPU throttled period intervals.",
		LabelKeys: baseLabelKeys,
	}
	containerCPUCfsThrottledSecondsTotal = &runtime.MetricDescriptor{
		Name:      "container_cpu_cfs_throttled_seconds_total",
		Help:      "Total time duration the container has been throttled.",
		LabelKeys: baseLabelKeys,
	}
)

// Memory metrics
var (
	containerMemoryCache = &runtime.MetricDescriptor{
		Name:      "container_memory_cache",
		Help:      "Number of bytes of memory used by the container for caching purposes.",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryRss = &runtime.MetricDescriptor{
		Name:      "container_memory_rss",
		Help:      "Size of RSS in bytes, total anonymous memory and [swap cache memory used by the container.",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryKernelUsage = &runtime.MetricDescriptor{
		Name:      "container_memory_kernel_usage",
		Help:      "Size of kernel memory allocated to the container in bytes, includes page tables, socket buffers...",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryMappedFile = &runtime.MetricDescriptor{
		Name:      "container_memory_mapped_file",
		Help:      "Size of container's memory mapped files in bytes.",
		LabelKeys: baseLabelKeys,
	}
	containerMemorySwap = &runtime.MetricDescriptor{
		Name:      "container_memory_swap",
		Help:      "Container swap usage in bytes.",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryFailcnt = &runtime.MetricDescriptor{
		Name:      "container_memory_failcnt",
		Help:      "Number of times container memory usage has hit its cgroup v1 upper memory limit, for cg2 this value is not currently supported",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryUsageBytes = &runtime.MetricDescriptor{
		Name:      "container_memory_usage_bytes",
		Help:      "Current memory usage in bytes, including all memory regardless of when it was accessed",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryMaxUsageBytes = &runtime.MetricDescriptor{
		Name:      "container_memory_max_usage_bytes",
		Help:      "Maximum container memory usage recorded in bytes",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryWorkingSetBytes = &runtime.MetricDescriptor{
		Name:      "container_memory_working_set_bytes",
		Help:      "Amount of memory that is actively used by the container, excluding cached memory.",
		LabelKeys: baseLabelKeys,
	}
	// additional added from cadvisor
	containerMemoryTotalActiveFileBytes = &runtime.MetricDescriptor{
		Name:      "container_memory_total_active_file_bytes",
		Help:      "Amount of cache memory bytes, in use by the container, in file on disk.",
		LabelKeys: baseLabelKeys,
	}
	// additional added from cadvisor
	containerMemoryTotalInactiveFileBytes = &runtime.MetricDescriptor{
		Name:      "container_memory_total_inactive_file_bytes",
		Help:      "Amount of inactive cache memory bytes, for the container, in file on disk.",
		LabelKeys: baseLabelKeys,
	}
	containerMemoryFailuresTotal = &runtime.MetricDescriptor{
		Name:      "container_memory_failures_total",
		Help:      "Cumulative count of container memory allocation failures.",
		LabelKeys: append(append([]string(nil), baseLabelKeys...), "failure_type", "scope"),
	}
	containerOomEventsTotal = &runtime.MetricDescriptor{
		Name:      "container_oom_events_total",
		Help:      "Count of out of memory events observed for the container",
		LabelKeys: baseLabelKeys,
	}
)

// Disk metrics
var (
	containerFsInodesFree = &runtime.MetricDescriptor{
		Name:      "container_fs_inodes_free",
		Help:      "Number of available Inodes",
		LabelKeys: diskLabelKeys,
	}
	containerFsInodesTotal = &runtime.MetricDescriptor{
		Name:      "container_fs_inodes_total",
		Help:      "Number of Inodes",
		LabelKeys: diskLabelKeys,
	}
	containerFsLimitBytes = &runtime.MetricDescriptor{
		Name:      "container_fs_limit_bytes",
		Help:      "Number of bytes that can be consumed by the container on this filesystem.",
		LabelKeys: diskLabelKeys,
	}
	containerFsUsageBytes = &runtime.MetricDescriptor{
		Name:      "container_fs_usage_bytes",
		Help:      "Number of bytes that are consumed by the container on this filesystem.",
		LabelKeys: diskLabelKeys,
	}
)

// Disk IO metrics
var (
	containerFsReadsBytesTotal = &runtime.MetricDescriptor{
		Name:      "container_fs_reads_bytes_total",
		Help:      "Cumulative count of bytes read",
		LabelKeys: diskLabelKeys,
	}
	containerFsReadsTotal = &runtime.MetricDescriptor{
		Name:      "container_fs_reads_total",
		Help:      "Cumulative count of reads completed",
		LabelKeys: diskLabelKeys,
	}
	containerFsWritesBytesTotal = &runtime.MetricDescriptor{
		Name:      "container_fs_writes_bytes_total",
		Help:      "Cumulative count of bytes written",
		LabelKeys: diskLabelKeys,
	}
	containerFsWritesTotal = &runtime.MetricDescriptor{
		Name:      "container_fs_writes_total",
		Help:      "Cumulative count of writes completed",
		LabelKeys: diskLabelKeys,
	}
	containerBlkioDeviceUsageTotal = &runtime.MetricDescriptor{
		Name:      "container_blkio_device_usage_total",
		Help:      "Blkio device bytes usage",
		LabelKeys: append(append([]string(nil), diskLabelKeys...), "major", "minor", "operation"),
	}
)

// Network metrics
var (
	containerNetworkReceiveBytesTotal = &runtime.MetricDescriptor{
		Name:      "container_network_receive_bytes_total",
		Help:      "Cumulative count of bytes received",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkReceivePacketsTotal = &runtime.MetricDescriptor{
		Name:      "container_network_receive_packets_total",
		Help:      "Cumulative count of packets received",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkReceivePacketsDroppedTotal = &runtime.MetricDescriptor{
		Name:      "container_network_receive_packets_dropped_total",
		Help:      "Cumulative count of packets dropped while receiving",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkReceiveErrorsTotal = &runtime.MetricDescriptor{
		Name:      "container_network_receive_errors_total",
		Help:      "Cumulative count of errors encountered while receiving",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkTransmitBytesTotal = &runtime.MetricDescriptor{
		Name:      "container_network_transmit_bytes_total",
		Help:      "Cumulative count of bytes transmitted",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkTransmitPacketsTotal = &runtime.MetricDescriptor{
		Name:      "container_network_transmit_packets_total",
		Help:      "Cumulative count of packets transmitted",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkTransmitPacketsDroppedTotal = &runtime.MetricDescriptor{
		Name:      "container_network_transmit_packets_dropped_total",
		Help:      "Cumulative count of packets dropped while transmitting",
		LabelKeys: networkLabelKeys,
	}
	containerNetworkTransmitErrorsTotal = &runtime.MetricDescriptor{
		Name:      "container_network_transmit_errors_total",
		Help:      "Cumulative count of errors encountered while transmitting",
		LabelKeys: networkLabelKeys,
	}
)

// Process metrics
var (
	containerProcesses = &runtime.MetricDescriptor{
		Name:      "container_processes",
		Help:      "Number of processes running inside the container.",
		LabelKeys: baseLabelKeys,
	}
	containerFileDescriptors = &runtime.MetricDescriptor{
		Name:      "container_file_descriptors",
		Help:      "Number of open file descriptors for the container.",
		LabelKeys: baseLabelKeys,
	}
	containerSockets = &runtime.MetricDescriptor{
		Name:      "container_sockets",
		Help:      "Number of open sockets for the container.",
		LabelKeys: baseLabelKeys,
	}
	containerThreadsMax = &runtime.MetricDescriptor{
		Name:      "container_threads_max",
		Help:      "Maximum number of threads allowed inside the container, infinity if value is zero",
		LabelKeys: baseLabelKeys,
	}
	containerThreads = &runtime.MetricDescriptor{
		Name:      "container_threads",
		Help:      "Number of threads running inside the container",
		LabelKeys: baseLabelKeys,
	}
	containerUlimitsSoft = &runtime.MetricDescriptor{
		Name:      "container_ulimits_soft",
		Help:      "Soft ulimit values for the container root process. Unlimited if -1, except priority and nice",
		LabelKeys: append(append([]string(nil), baseLabelKeys...), "ulimit"),
	}
)

// Miscellaneous metrics
var (
	containerLastSeen = &runtime.MetricDescriptor{
		Name:      "container_last_seen",
		Help:      "Last time a container was seen by the exporter",
		LabelKeys: baseLabelKeys,
	}
	containerStartTimeSeconds = &runtime.MetricDescriptor{
		Name:      "container_start_time_seconds",
		Help:      "Start time of the container since unix epoch in seconds",
		LabelKeys: baseLabelKeys,
	}
)

// Container spec metrics
var (
	containerSpecCPUPeriod = &runtime.MetricDescriptor{
		Name:      "container_spec_cpu_period",
		Help:      "CPU period of the container",
		LabelKeys: baseLabelKeys,
	}
	containerSpecCPUShares = &runtime.MetricDescriptor{
		Name:      "container_spec_cpu_shares",
		Help:      "CPU share of the container",
		LabelKeys: baseLabelKeys,
	}
	containerSpecMemoryLimitBytes = &runtime.MetricDescriptor{
		Name:      "container_spec_memory_limit_bytes",
		Help:      "Memory limit for the container",
		LabelKeys: baseLabelKeys,
	}
	containerSpecMemoryReservationLimitBytes = &runtime.MetricDescriptor{
		Name:      "container_spec_memory_reservation_limit_bytes",
		Help:      "Memory reservation limit for the container",
		LabelKeys: baseLabelKeys,
	}
	containerSpecMemorySwapLimitBytes = &runtime.MetricDescriptor{
		Name:      "container_spec_memory_swap_limit_bytes",
		Help:      "Memory swap limit for the container",
		LabelKeys: baseLabelKeys,
	}
)

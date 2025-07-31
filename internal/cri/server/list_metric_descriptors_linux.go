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

var baseLabelKeys = []string{"id", "name"}

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
			{
				Name:      "container_cpu_user_seconds_total",
				Help:      "Cumulative user CPU time consumed in seconds.",
				LabelKeys: baseLabelKeys,
			}, {
				Name:      "container_cpu_system_seconds_total",
				Help:      "Cumulative system CPU time consumed in seconds.",
				LabelKeys: baseLabelKeys,
			}, {
				Name:      "container_cpu_usage_seconds_total",
				Help:      "Cumulative CPU time consumed in seconds.",
				LabelKeys: baseLabelKeys,
			}, {
				Name:      "container_cpu_cfs_periods_total",
				Help:      "Number of elapsed enforcement period intervals.",
				LabelKeys: baseLabelKeys,
			}, {
				Name:      "container_cpu_cfs_throttled_periods_total",
				Help:      "Number of throttled period intervals.",
				LabelKeys: baseLabelKeys,
			}, {
				Name:      "container_cpu_cfs_throttled_seconds_total",
				Help:      "Total time duration the container has been throttled.",
				LabelKeys: baseLabelKeys,
			},
		},
		MemoryUsageMetrics: {
			{
				Name:      "container_memory_cache",
				Help:      "Number of bytes of page cache memory.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_rss",
				Help:      "Size of RSS in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_kernel_usage",
				Help:      "Size of kernel memory allocated in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_mapped_file",
				Help:      "Size of memory mapped files in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_swap",
				Help:      "Container swap usage in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_failcnt",
				Help:      "Number of times container memory usage has hit its cgroup v1 upper memory limit, for cg2 this value is not currently supported",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_usage_bytes",
				Help:      "Current memory usage in bytes, including all memory regardless of when it was accessed",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_max_usage_bytes",
				Help:      "Maximum memory usage recorded in bytes",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_working_set_bytes",
				Help:      "Current working set in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				// additional added from cadvisor
				Name:      "container_memory_total_active_file_bytes",
				Help:      "Current total active cache memory in file on disk, in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				// additional added from cadvisor
				Name:      "container_memory_total_inactive_file_bytes",
				Help:      "Current total inactive cache memory in file, in bytes.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_memory_failures_total",
				Help:      "Cumulative count of memory allocation failures.",
				LabelKeys: append(baseLabelKeys, "failure_type", "scope"),
			},
		},
		NetworkUsageMetrics: {
			{
				Name:      "container_network_receive_bytes_total",
				Help:      "Cumulative count of bytes received",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_receive_packets_total",
				Help:      "Cumulative count of packets received",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_receive_packets_dropped_total",
				Help:      "Cumulative count of packets dropped while receiving",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_receive_errors_total",
				Help:      "Cumulative count of errors encountered while receiving",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_transmit_bytes_total",
				Help:      "Cumulative count of bytes transmitted",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_transmit_packets_total",
				Help:      "Cumulative count of packets transmitted",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_transmit_packets_dropped_total",
				Help:      "Cumulative count of packets dropped while transmitting",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
			{
				Name:      "container_network_transmit_errors_total",
				Help:      "Cumulative count of errors encountered while transmitting",
				LabelKeys: append(baseLabelKeys, "interface"),
			},
		},
		DiskUsageMetrics: {
			{
				Name:      "container_fs_inodes_free",
				Help:      "Number of available Inodes",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_inodes_total",
				Help:      "Number of Inodes",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_limit_bytes",
				Help:      "Number of bytes that can be consumed by the container on this filesystem.",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_usage_bytes",
				Help:      "Number of bytes that are consumed by the container on this filesystem.",
				LabelKeys: append(baseLabelKeys, "device"),
			},
		},
		DiskIOMetrics: {
			{
				Name:      "container_fs_reads_bytes_total",
				Help:      "Cumulative count of bytes read",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_reads_total",
				Help:      "Cumulative count of reads completed",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_sector_reads_total",
				Help:      "Cumulative count of sector reads completed",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_reads_merged_total",
				Help:      "Cumulative count of reads merged",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_read_seconds_total",
				Help:      "Cumulative count of seconds spent reading",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_writes_bytes_total",
				Help:      "Cumulative count of bytes written",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_writes_total",
				Help:      "Cumulative count of writes completed",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_sector_writes_total",
				Help:      "Cumulative count of sector writes completed",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_writes_merged_total",
				Help:      "Cumulative count of writes merged",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_write_seconds_total",
				Help:      "Cumulative count of seconds spent writing",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_io_current",
				Help:      "Number of I/Os currently in progress",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_io_time_seconds_total",
				Help:      "Cumulative count of seconds spent doing I/Os",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_fs_io_time_weighted_seconds_total",
				Help:      "Cumulative weighted I/O time in seconds",
				LabelKeys: append(baseLabelKeys, "device"),
			},
			{
				Name:      "container_blkio_device_usage_total",
				Help:      "Blkio device bytes usage",
				LabelKeys: append(baseLabelKeys, "device", "major", "minor", "operation"),
			},
		},
		ProcessMetrics: {
			{
				Name:      "container_processes",
				Help:      "Number of processes running inside the container.",
				LabelKeys: baseLabelKeys,
			},
			/*{
				Name:      "container_file_descriptors",
				Help:      "Number of open file descriptors for the container.",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_sockets",
				Help:      "Number of open sockets for the container.",
				LabelKeys: baseLabelKeys,
			},*/
			{
				Name:      "container_threads_max",
				Help:      "Maximum number of threads allowed inside the container, infinity if value is zero",
				LabelKeys: baseLabelKeys,
			},
			/*{
				Name:      "container_threads",
				Help:      "Number of threads running inside the container",
				LabelKeys: baseLabelKeys,
			},
			{
				Name:      "container_ulimits_soft",
				Help:      "Soft ulimit values for the container root process. Unlimited if -1, except priority and nice",
				LabelKeys: append(baseLabelKeys, "ulimit"),
			},*/
		},
	}
	return descriptors
}

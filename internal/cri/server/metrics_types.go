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
	"sync"
	"time"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type MetricsServer struct {
	collectionPeriod time.Duration
	sandboxMetrics   map[string]*SandboxMetrics
	mu               sync.RWMutex
}

type SandboxMetrics struct {
	metric *runtime.PodSandboxMetrics
}

type metricValue struct {
	value      uint64
	labels     []string
	metricType runtime.MetricType
}

type metricValues []metricValue

type containerMetric struct {
	desc      *runtime.MetricDescriptor
	valueFunc func() metricValues
}

type containerCPUMetrics struct {
	UsageUsec          uint64
	UserUsec           uint64
	SystemUsec         uint64
	NRPeriods          uint64
	NRThrottledPeriods uint64
	ThrottledUsec      uint64
	//LoadAverage10      uint64
	//TasksState         uint64
}

type containerMemoryMetrics struct {
	Cache        uint64
	RSS          uint64
	Swap         uint64
	KernelUsage  uint64
	FileMapped   uint64
	FailCount    uint64
	MemoryUsage  uint64
	MaxUsage     uint64
	WorkingSet   uint64
	ActiveFile   uint64
	InactiveFile uint64
	PgFault      uint64
	PgMajFault   uint64
}

type containerNetworkMetrics struct {
	Name      string
	RxBytes   uint64
	RxPackets uint64
	RxErrors  uint64
	RxDropped uint64
	TxBytes   uint64
	TxPackets uint64
	TxErrors  uint64
	TxDropped uint64
}

type containerPerDiskStats struct {
	Device string            `json:"device"`
	Major  uint64            `json:"major"`
	Minor  uint64            `json:"minor"`
	Stats  map[string]uint64 `json:"stats"`
}

type containerDiskIoMetrics struct {
	IoServiceBytes []containerPerDiskStats `json:"io_service_bytes,omitempty"`
	IoServiced     []containerPerDiskStats `json:"io_serviced,omitempty"`
	IoQueued       []containerPerDiskStats `json:"io_queued,omitempty"`
	Sectors        []containerPerDiskStats `json:"sectors,omitempty"`
	IoServiceTime  []containerPerDiskStats `json:"io_service_time,omitempty"`
	IoWaitTime     []containerPerDiskStats `json:"io_wait_time,omitempty"`
	IoMerged       []containerPerDiskStats `json:"io_merged,omitempty"`
	IoTime         []containerPerDiskStats `json:"io_time,omitempty"`
}

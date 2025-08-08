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

type containerFilesystemMetrics struct {
	Device          string
	Type            string
	Limit           uint64
	Usage           uint64
	BaseUsage       uint64
	Available       uint64
	Inodes          uint64
	InodesFree      uint64
	InodesUsed      uint64
	ReadsCompleted  uint64
	ReadsMerged     uint64
	SectorsRead     uint64
	ReadTime        uint64
	WritesCompleted uint64
	WritesMerged    uint64
	SectorsWritten  uint64
	WriteTime       uint64
	IoInProgress    uint64
	IoTime          uint64
	WeightedIoTime  uint64
}

/*
Copyright 2017 The Kubernetes Authors.

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
	"testing"

	"github.com/golang/protobuf/proto"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestToOCIResources(t *testing.T) {
	resources := &runtime.LinuxContainerResources{
		CpuPeriod:          10000,
		CpuQuota:           20000,
		CpuShares:          300,
		MemoryLimitInBytes: 4000000,
		OomScoreAdj:        -500,
		CpusetCpus:         "6,7",
		CpusetMems:         "8,9",
	}
	expected := &runtimespec.LinuxResources{
		CPU: &runtimespec.LinuxCPU{
			Period: proto.Uint64(10000),
			Quota:  proto.Int64(20000),
			Shares: proto.Uint64(300),
			Cpus:   "6,7",
			Mems:   "8,9",
		},
		Memory: &runtimespec.LinuxMemory{
			Limit: proto.Int64(4000000),
		},
	}
	assert.Equal(t, expected, toOCIResources(resources))
}

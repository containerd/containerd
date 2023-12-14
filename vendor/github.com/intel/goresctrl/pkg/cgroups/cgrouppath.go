// Copyright 2020 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cgroups

import (
	goresctrlpath "github.com/intel/goresctrl/pkg/path"
)

// nolint
const (
	// Tasks is a cgroup's "tasks" entry.
	Tasks = "tasks"
	// Procs is cgroup's "cgroup.procs" entry.
	Procs = "cgroup.procs"
	// CpuShares is the cpu controller's "cpu.shares" entry.
	CpuShares = "cpu.shares"
	// CpuPeriod is the cpu controller's "cpu.cfs_period_us" entry.
	CpuPeriod = "cpu.cfs_period_us"
	// CpuQuota is the cpu controller's "cpu.cfs_quota_us" entry.
	CpuQuota = "cpu.cfs_quota_us"
	// CpusetCpus is the cpuset controller's cpuset.cpus entry.
	CpusetCpus = "cpuset.cpus"
	// CpusetMems is the cpuset controller's cpuset.mems entry.
	CpusetMems = "cpuset.mems"
)

const cgroupBasePath = "sys/fs/cgroup"

// GetMountDir returns the common mount point for cgroup v1 controllers.
func GetMountDir() string {
	return cgroupPath()
}

// GetV2Dir returns the cgroup v2 unified mount directory.
func GetV2Dir() string {
	return cgroupPath("unified")
}

func cgroupPath(elems ...string) string {
	return goresctrlpath.Path(append([]string{cgroupBasePath}, elems...)...)
}

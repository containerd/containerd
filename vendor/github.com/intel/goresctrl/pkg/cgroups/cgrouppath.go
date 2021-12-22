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
	"path"
	"path/filepath"
)

//nolint
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

var (
	// mount is the parent directory for per-controller cgroupfs mounts.
	mountDir = "/sys/fs/cgroup"
	// v2Dir is the parent directory for per-controller cgroupfs mounts.
	v2Dir = path.Join(mountDir, "unified")
	// KubeletRoot is the --cgroup-root option the kubelet is running with.
	KubeletRoot = ""
)

// GetMountDir returns the common mount point for cgroup v1 controllers.
func GetMountDir() string {
	return mountDir
}

// SetMountDir sets the common mount point for the cgroup v1 controllers.
func SetMountDir(dir string) {
	v2, _ := filepath.Rel(mountDir, v2Dir)
	mountDir = dir
	if v2 != "" {
		v2Dir = path.Join(mountDir, v2)
	}
}

// GetV2Dir returns the cgroup v2 unified mount directory.
func GetV2Dir() string {
	return v2Dir
}

// SetV2Dir sets the unified cgroup v2 mount directory.
func SetV2Dir(dir string) {
	if dir[0] == '/' {
		v2Dir = dir
	} else {
		v2Dir = path.Join(mountDir, v2Dir)
	}
}

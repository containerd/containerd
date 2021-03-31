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

package runc

import (
	"github.com/containerd/cgroups"
	cgroupsv2 "github.com/containerd/cgroups/v2"
	"github.com/containerd/containerd/pkg/process"
	"github.com/sirupsen/logrus"
)

func newContainerCgroup(pid int, container *Container) {
	var cg interface{}
	if cgroups.Mode() == cgroups.Unified {
		g, err := cgroupsv2.PidGroupPath(pid)
		if err != nil {
			logrus.WithError(err).Errorf("loading cgroup2 for %d", pid)
			return
		}
		cg, err = cgroupsv2.LoadManager("/sys/fs/cgroup", g)
		if err != nil {
			logrus.WithError(err).Errorf("loading cgroup2 for %d", pid)
		}
	} else {
		g, err := cgroups.Load(cgroups.V1, cgroups.PidPath(pid))
		if err != nil {
			logrus.WithError(err).Errorf("loading cgroup for %d", pid)
		}
		cg = g
	}
	container.cgroup = cg
}

func (c *Container) startCgroup(p process.Process) {
	if c.Cgroup() == nil && p.Pid() > 0 {
		var cg interface{}
		if cgroups.Mode() == cgroups.Unified {
			g, err := cgroupsv2.PidGroupPath(p.Pid())
			if err != nil {
				logrus.WithError(err).Errorf("loading cgroup2 for %d", p.Pid())
			}
			cg, err = cgroupsv2.LoadManager("/sys/fs/cgroup", g)
			if err != nil {
				logrus.WithError(err).Errorf("loading cgroup2 for %d", p.Pid())
			}
		} else {
			g, err := cgroups.Load(cgroups.V1, cgroups.PidPath(p.Pid()))
			if err != nil {
				logrus.WithError(err).Errorf("loading cgroup for %d", p.Pid())
			}
			cg = g
		}
		c.cgroup = cg
	}
}

//go:build linux

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

package nri

import (
	nri "github.com/containerd/nri/pkg/adaptation"
)

func containerToNRI(ctr Container) *nri.Container {
	nriCtr := commonContainerToNRI(ctr)
	lnxCtr := ctr.GetLinuxContainer()
	nriCtr.Linux = &nri.LinuxContainer{
		Namespaces:     lnxCtr.GetLinuxNamespaces(),
		Devices:        lnxCtr.GetLinuxDevices(),
		Resources:      lnxCtr.GetLinuxResources(),
		OomScoreAdj:    nri.Int(lnxCtr.GetOOMScoreAdj()),
		CgroupsPath:    lnxCtr.GetCgroupsPath(),
		IoPriority:     lnxCtr.GetIOPriority(),
		Scheduler:      lnxCtr.GetScheduler(),
		NetDevices:     lnxCtr.GetNetDevices(),
		Rdt:            lnxCtr.GetRdt(),
		SeccompProfile: lnxCtr.GetSeccompProfile(),
	}
	return nriCtr
}

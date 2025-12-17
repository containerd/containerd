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

// Container interface for interacting with NRI.
type Container interface {
	GetDomain() string

	GetPodSandboxID() string
	GetID() string
	GetName() string
	GetState() nri.ContainerState
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	GetArgs() []string
	GetEnv() []string
	GetMounts() []*nri.Mount
	GetHooks() *nri.Hooks
	GetLinuxContainer() LinuxContainer

	GetPid() uint32
}

type LinuxContainer interface {
	GetLinuxNamespaces() []*nri.LinuxNamespace
	GetLinuxDevices() []*nri.LinuxDevice
	GetLinuxResources() *nri.LinuxResources
	GetOOMScoreAdj() *int
	GetCgroupsPath() string
	GetIOPriority() *nri.LinuxIOPriority
	GetScheduler() *nri.LinuxScheduler
	GetNetDevices() map[string]*nri.LinuxNetDevice
	GetRdt() *nri.LinuxRdt
	GetSeccompProfile() *nri.SecurityProfile
}

func commonContainerToNRI(ctr Container) *nri.Container {
	return &nri.Container{
		Id:           ctr.GetID(),
		PodSandboxId: ctr.GetPodSandboxID(),
		Name:         ctr.GetName(),
		State:        ctr.GetState(),
		Labels:       ctr.GetLabels(),
		Annotations:  ctr.GetAnnotations(),
		Args:         ctr.GetArgs(),
		Env:          ctr.GetEnv(),
		Mounts:       ctr.GetMounts(),
		Hooks:        ctr.GetHooks(),
		Pid:          ctr.GetPid(),
	}
}

func containersToNRI(ctrList []Container) []*nri.Container {
	ctrs := make([]*nri.Container, len(ctrList))
	for i, ctr := range ctrList {
		ctrs[i] = containerToNRI(ctr)
	}
	return ctrs
}

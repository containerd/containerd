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
	"fmt"
	"strings"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
)

// specGen implements nrigen.UnderlyingGenerator directly on an *rspec.Spec,
// removing the need to import github.com/opencontainers/runtime-tools/generate.
type specGen struct {
	spec *rspec.Spec
}

func newSpecGen(spec *rspec.Spec) *specGen {
	return &specGen{spec: spec}
}

func (g *specGen) Spec() *rspec.Spec {
	return g.spec
}

func (g *specGen) initConfig() {
	if g.spec == nil {
		g.spec = &rspec.Spec{}
	}
}

func (g *specGen) initProcess() {
	g.initConfig()
	if g.spec.Process == nil {
		g.spec.Process = &rspec.Process{}
	}
}

func (g *specGen) initLinux() {
	g.initConfig()
	if g.spec.Linux == nil {
		g.spec.Linux = &rspec.Linux{}
	}
}

func (g *specGen) initLinuxResources() {
	g.initLinux()
	if g.spec.Linux.Resources == nil {
		g.spec.Linux.Resources = &rspec.LinuxResources{}
	}
}

func (g *specGen) initLinuxCPU() {
	g.initLinuxResources()
	if g.spec.Linux.Resources.CPU == nil {
		g.spec.Linux.Resources.CPU = &rspec.LinuxCPU{}
	}
}

func (g *specGen) initLinuxMemory() {
	g.initLinuxResources()
	if g.spec.Linux.Resources.Memory == nil {
		g.spec.Linux.Resources.Memory = &rspec.LinuxMemory{}
	}
}

func (g *specGen) initHooks() {
	g.initConfig()
	if g.spec.Hooks == nil {
		g.spec.Hooks = &rspec.Hooks{}
	}
}

func (g *specGen) initAnnotations() {
	g.initConfig()
	if g.spec.Annotations == nil {
		g.spec.Annotations = map[string]string{}
	}
}

func (g *specGen) initSysctl() {
	g.initLinux()
	if g.spec.Linux.Sysctl == nil {
		g.spec.Linux.Sysctl = map[string]string{}
	}
}

func (g *specGen) AddAnnotation(key, value string) {
	g.initAnnotations()
	g.spec.Annotations[key] = value
}

func (g *specGen) RemoveAnnotation(key string) {
	if g.spec != nil && g.spec.Annotations != nil {
		delete(g.spec.Annotations, key)
	}
}

func (g *specGen) AddDevice(device rspec.LinuxDevice) {
	g.initLinux()
	for i, d := range g.spec.Linux.Devices {
		if d.Path == device.Path {
			g.spec.Linux.Devices[i] = device
			return
		}
	}
	g.spec.Linux.Devices = append(g.spec.Linux.Devices, device)
}

func (g *specGen) RemoveDevice(path string) {
	if g.spec == nil || g.spec.Linux == nil {
		return
	}
	for i, d := range g.spec.Linux.Devices {
		if d.Path == path {
			g.spec.Linux.Devices = append(g.spec.Linux.Devices[:i], g.spec.Linux.Devices[i+1:]...)
			return
		}
	}
}

func (g *specGen) AddOrReplaceLinuxNamespace(ns string, path string) error {
	nsType, err := namespaceType(ns)
	if err != nil {
		return err
	}
	g.initLinux()
	for i, n := range g.spec.Linux.Namespaces {
		if n.Type == nsType {
			g.spec.Linux.Namespaces[i].Path = path
			return nil
		}
	}
	g.spec.Linux.Namespaces = append(g.spec.Linux.Namespaces, rspec.LinuxNamespace{Type: nsType, Path: path})
	return nil
}

func (g *specGen) RemoveLinuxNamespace(ns string) error {
	nsType, err := namespaceType(ns)
	if err != nil {
		return err
	}
	if g.spec == nil || g.spec.Linux == nil {
		return nil
	}
	for i, n := range g.spec.Linux.Namespaces {
		if n.Type == nsType {
			g.spec.Linux.Namespaces = append(g.spec.Linux.Namespaces[:i], g.spec.Linux.Namespaces[i+1:]...)
			return nil
		}
	}
	return nil
}

func namespaceType(ns string) (rspec.LinuxNamespaceType, error) {
	switch ns {
	case "network":
		return rspec.NetworkNamespace, nil
	case "pid":
		return rspec.PIDNamespace, nil
	case "mount":
		return rspec.MountNamespace, nil
	case "ipc":
		return rspec.IPCNamespace, nil
	case "uts":
		return rspec.UTSNamespace, nil
	case "user":
		return rspec.UserNamespace, nil
	case "cgroup":
		return rspec.CgroupNamespace, nil
	case "time":
		return rspec.TimeNamespace, nil
	default:
		return "", fmt.Errorf("unrecognized namespace %q", ns)
	}
}

func (g *specGen) AddPreStartHook(hook rspec.Hook) {
	g.initHooks()
	g.spec.Hooks.Prestart = append(g.spec.Hooks.Prestart, hook) //nolint:staticcheck
}

func (g *specGen) AddPostStartHook(hook rspec.Hook) {
	g.initHooks()
	g.spec.Hooks.Poststart = append(g.spec.Hooks.Poststart, hook)
}

func (g *specGen) AddPostStopHook(hook rspec.Hook) {
	g.initHooks()
	g.spec.Hooks.Poststop = append(g.spec.Hooks.Poststop, hook)
}

func (g *specGen) AddProcessEnv(name, value string) {
	if name == "" {
		return
	}
	g.initProcess()
	prefix := name + "="
	for i, e := range g.spec.Process.Env {
		if strings.HasPrefix(e, prefix) {
			g.spec.Process.Env[i] = prefix + value
			return
		}
	}
	g.spec.Process.Env = append(g.spec.Process.Env, prefix+value)
}

func (g *specGen) ClearProcessEnv() {
	if g.spec == nil || g.spec.Process == nil {
		return
	}
	g.spec.Process.Env = []string{}
}

func (g *specGen) SetProcessArgs(args []string) {
	g.initProcess()
	g.spec.Process.Args = args
}

func (g *specGen) SetProcessOOMScoreAdj(adj int) {
	g.initProcess()
	g.spec.Process.OOMScoreAdj = &adj
}

func (g *specGen) AddMount(mnt rspec.Mount) {
	g.initConfig()
	g.spec.Mounts = append(g.spec.Mounts, mnt)
}

func (g *specGen) RemoveMount(dest string) {
	if g.spec == nil {
		return
	}
	for i, m := range g.spec.Mounts {
		if m.Destination == dest {
			g.spec.Mounts = append(g.spec.Mounts[:i], g.spec.Mounts[i+1:]...)
			return
		}
	}
}

func (g *specGen) ClearMounts() {
	if g.spec == nil {
		return
	}
	g.spec.Mounts = []rspec.Mount{}
}

func (g *specGen) Mounts() []rspec.Mount {
	if g.spec == nil {
		return nil
	}
	return g.spec.Mounts
}

func (g *specGen) AddLinuxSysctl(key, value string) {
	g.initSysctl()
	g.spec.Linux.Sysctl[key] = value
}

func (g *specGen) RemoveLinuxSysctl(key string) {
	if g.spec == nil || g.spec.Linux == nil || g.spec.Linux.Sysctl == nil {
		return
	}
	delete(g.spec.Linux.Sysctl, key)
}

func (g *specGen) SetLinuxCgroupsPath(path string) {
	g.initLinux()
	g.spec.Linux.CgroupsPath = path
}

func (g *specGen) SetLinuxRootPropagation(rp string) error {
	switch rp {
	case "", "private", "rprivate", "slave", "rslave", "shared", "rshared", "unbindable", "runbindable":
	default:
		return fmt.Errorf("rootfs-propagation %q must be empty or one of (r)private|(r)slave|(r)shared|(r)unbindable", rp)
	}
	g.initLinux()
	g.spec.Linux.RootfsPropagation = rp
	return nil
}

func (g *specGen) AddLinuxResourcesDevice(allow bool, devType string, major, minor *int64, access string) {
	g.initLinuxResources()
	g.spec.Linux.Resources.Devices = append(g.spec.Linux.Resources.Devices, rspec.LinuxDeviceCgroup{
		Allow:  allow,
		Type:   devType,
		Major:  major,
		Minor:  minor,
		Access: access,
	})
}

func (g *specGen) AddLinuxResourcesHugepageLimit(pageSize string, limit uint64) {
	g.initLinuxResources()
	for i, h := range g.spec.Linux.Resources.HugepageLimits {
		if h.Pagesize == pageSize {
			g.spec.Linux.Resources.HugepageLimits[i].Limit = limit
			return
		}
	}
	g.spec.Linux.Resources.HugepageLimits = append(g.spec.Linux.Resources.HugepageLimits,
		rspec.LinuxHugepageLimit{Pagesize: pageSize, Limit: limit})
}

func (g *specGen) AddLinuxResourcesUnified(key, val string) {
	g.initLinuxResources()
	if g.spec.Linux.Resources.Unified == nil {
		g.spec.Linux.Resources.Unified = map[string]string{}
	}
	g.spec.Linux.Resources.Unified[key] = val
}

func (g *specGen) SetLinuxResourcesCPUShares(shares uint64) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.Shares = &shares
}

func (g *specGen) SetLinuxResourcesCPUQuota(quota int64) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.Quota = &quota
}

func (g *specGen) SetLinuxResourcesCPUPeriod(period uint64) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.Period = &period
}

func (g *specGen) SetLinuxResourcesCPURealtimeRuntime(time int64) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.RealtimeRuntime = &time
}

func (g *specGen) SetLinuxResourcesCPURealtimePeriod(period uint64) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.RealtimePeriod = &period
}

func (g *specGen) SetLinuxResourcesCPUCpus(cpus string) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.Cpus = cpus
}

func (g *specGen) SetLinuxResourcesCPUMems(mems string) {
	g.initLinuxCPU()
	g.spec.Linux.Resources.CPU.Mems = mems
}

func (g *specGen) SetLinuxResourcesMemoryLimit(limit int64) {
	g.initLinuxMemory()
	g.spec.Linux.Resources.Memory.Limit = &limit
}

func (g *specGen) SetLinuxResourcesMemorySwap(swap int64) {
	g.initLinuxMemory()
	g.spec.Linux.Resources.Memory.Swap = &swap
}

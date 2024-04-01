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

package api

import (
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// FromOCILinuxResources returns resources from an OCI runtime Spec.
func FromOCILinuxResources(o *rspec.LinuxResources, ann map[string]string) *LinuxResources {
	if o == nil {
		return nil
	}
	l := &LinuxResources{}
	if m := o.Memory; m != nil {
		l.Memory = &LinuxMemory{
			Limit:            Int64(m.Limit),
			Reservation:      Int64(m.Reservation),
			Swap:             Int64(m.Swap),
			Kernel:           Int64(m.Kernel),
			KernelTcp:        Int64(m.KernelTCP),
			Swappiness:       UInt64(m.Swappiness),
			DisableOomKiller: Bool(m.DisableOOMKiller),
			UseHierarchy:     Bool(m.UseHierarchy),
		}
	}
	if c := o.CPU; c != nil {
		l.Cpu = &LinuxCPU{
			Shares:          UInt64(c.Shares),
			Quota:           Int64(c.Quota),
			Period:          UInt64(c.Period),
			RealtimeRuntime: Int64(c.RealtimeRuntime),
			RealtimePeriod:  UInt64(c.RealtimePeriod),
			Cpus:            c.Cpus,
			Mems:            c.Mems,
		}
	}
	for _, h := range o.HugepageLimits {
		l.HugepageLimits = append(l.HugepageLimits, &HugepageLimit{
			PageSize: h.Pagesize,
			Limit:    h.Limit,
		})
	}
	for _, d := range o.Devices {
		l.Devices = append(l.Devices, &LinuxDeviceCgroup{
			Allow:  d.Allow,
			Type:   d.Type,
			Major:  Int64(d.Major),
			Minor:  Int64(d.Minor),
			Access: d.Access,
		})
	}
	return l
}

func FromCRILinuxResources(c *cri.LinuxContainerResources) *LinuxResources {
	if c == nil {
		return nil
	}
	shares, quota, period := uint64(c.CpuShares), c.CpuQuota, uint64(c.CpuPeriod)
	r := &LinuxResources{
		Cpu: &LinuxCPU{
			Shares: UInt64(&shares),
			Quota:  Int64(&quota),
			Period: UInt64(&period),
			Cpus:   c.CpusetCpus,
			Mems:   c.CpusetMems,
		},
		Memory: &LinuxMemory{
			Limit: Int64(&c.MemoryLimitInBytes),
		},
	}
	for _, l := range c.HugepageLimits {
		r.HugepageLimits = append(r.HugepageLimits,
			&HugepageLimit{
				PageSize: l.PageSize,
				Limit:    l.Limit,
			})
	}
	return r
}

// ToOCI returns resources for an OCI runtime Spec.
func (r *LinuxResources) ToOCI() *rspec.LinuxResources {
	if r == nil {
		return nil
	}
	o := &rspec.LinuxResources{
		CPU:    &rspec.LinuxCPU{},
		Memory: &rspec.LinuxMemory{},
	}
	if r.Memory != nil {
		o.Memory = &rspec.LinuxMemory{
			Limit:            r.Memory.Limit.Get(),
			Reservation:      r.Memory.Reservation.Get(),
			Swap:             r.Memory.Swap.Get(),
			Kernel:           r.Memory.Kernel.Get(),
			KernelTCP:        r.Memory.KernelTcp.Get(),
			Swappiness:       r.Memory.Swappiness.Get(),
			DisableOOMKiller: r.Memory.DisableOomKiller.Get(),
			UseHierarchy:     r.Memory.UseHierarchy.Get(),
		}
	}
	if r.Cpu != nil {
		o.CPU = &rspec.LinuxCPU{
			Shares:          r.Cpu.Shares.Get(),
			Quota:           r.Cpu.Quota.Get(),
			Period:          r.Cpu.Period.Get(),
			RealtimeRuntime: r.Cpu.RealtimeRuntime.Get(),
			RealtimePeriod:  r.Cpu.RealtimePeriod.Get(),
			Cpus:            r.Cpu.Cpus,
			Mems:            r.Cpu.Mems,
		}
	}
	for _, l := range r.HugepageLimits {
		o.HugepageLimits = append(o.HugepageLimits, rspec.LinuxHugepageLimit{
			Pagesize: l.PageSize,
			Limit:    l.Limit,
		})
	}
	if len(r.Unified) != 0 {
		o.Unified = make(map[string]string)
		for k, v := range r.Unified {
			o.Unified[k] = v
		}
	}
	for _, d := range r.Devices {
		o.Devices = append(o.Devices, rspec.LinuxDeviceCgroup{
			Allow:  d.Allow,
			Type:   d.Type,
			Major:  d.Major.Get(),
			Minor:  d.Minor.Get(),
			Access: d.Access,
		})
	}

	return o
}

// ToCRI returns resources for CRI.
func (r *LinuxResources) ToCRI(oomScoreAdj int64) *cri.LinuxContainerResources {
	if r == nil {
		return nil
	}
	o := &cri.LinuxContainerResources{}
	if r.Memory != nil {
		o.MemoryLimitInBytes = r.Memory.GetLimit().GetValue()
		o.OomScoreAdj = oomScoreAdj
	}
	if r.Cpu != nil {
		o.CpuShares = int64(r.Cpu.GetShares().GetValue())
		o.CpuPeriod = int64(r.Cpu.GetPeriod().GetValue())
		o.CpuQuota = r.Cpu.GetQuota().GetValue()
		o.CpusetCpus = r.Cpu.Cpus
		o.CpusetMems = r.Cpu.Mems
	}
	for _, l := range r.HugepageLimits {
		o.HugepageLimits = append(o.HugepageLimits, &cri.HugepageLimit{
			PageSize: l.PageSize,
			Limit:    l.Limit,
		})
	}
	if len(r.Unified) != 0 {
		o.Unified = make(map[string]string)
		for k, v := range r.Unified {
			o.Unified[k] = v
		}
	}

	return o
}

// Copy creates a copy of the resources.
func (r *LinuxResources) Copy() *LinuxResources {
	if r == nil {
		return nil
	}
	o := &LinuxResources{}
	if r.Memory != nil {
		o.Memory = &LinuxMemory{
			Limit:            Int64(r.Memory.GetLimit()),
			Reservation:      Int64(r.Memory.GetReservation()),
			Swap:             Int64(r.Memory.GetSwap()),
			Kernel:           Int64(r.Memory.GetKernel()),
			KernelTcp:        Int64(r.Memory.GetKernelTcp()),
			Swappiness:       UInt64(r.Memory.GetSwappiness()),
			DisableOomKiller: Bool(r.Memory.GetDisableOomKiller()),
			UseHierarchy:     Bool(r.Memory.GetUseHierarchy()),
		}
	}
	if r.Cpu != nil {
		o.Cpu = &LinuxCPU{
			Shares:          UInt64(r.Cpu.GetShares()),
			Quota:           Int64(r.Cpu.GetQuota()),
			Period:          UInt64(r.Cpu.GetPeriod()),
			RealtimeRuntime: Int64(r.Cpu.GetRealtimeRuntime()),
			RealtimePeriod:  UInt64(r.Cpu.GetRealtimePeriod()),
			Cpus:            r.Cpu.GetCpus(),
			Mems:            r.Cpu.GetMems(),
		}
	}
	for _, l := range r.HugepageLimits {
		o.HugepageLimits = append(o.HugepageLimits, &HugepageLimit{
			PageSize: l.PageSize,
			Limit:    l.Limit,
		})
	}
	if len(r.Unified) != 0 {
		o.Unified = make(map[string]string)
		for k, v := range r.Unified {
			o.Unified[k] = v
		}
	}
	o.BlockioClass = String(r.BlockioClass)
	o.RdtClass = String(r.RdtClass)

	return o
}

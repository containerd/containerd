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

package adaptation

import (
	"fmt"
	"strings"

	"github.com/containerd/nri/pkg/api"
)

type result struct {
	request resultRequest
	reply   resultReply
	updates map[string]*ContainerUpdate
	owners  resultOwners
}

type resultRequest struct {
	create *CreateContainerRequest
	update *UpdateContainerRequest
}

type resultReply struct {
	adjust *ContainerAdjustment
	update []*ContainerUpdate
}

type resultOwners map[string]*owners

func collectCreateContainerResult(request *CreateContainerRequest) *result {
	if request.Container.Labels == nil {
		request.Container.Labels = map[string]string{}
	}
	if request.Container.Annotations == nil {
		request.Container.Annotations = map[string]string{}
	}
	if request.Container.Mounts == nil {
		request.Container.Mounts = []*Mount{}
	}
	if request.Container.Env == nil {
		request.Container.Env = []string{}
	}
	if request.Container.Hooks == nil {
		request.Container.Hooks = &Hooks{}
	}
	if request.Container.Rlimits == nil {
		request.Container.Rlimits = []*POSIXRlimit{}
	}
	if request.Container.Linux == nil {
		request.Container.Linux = &LinuxContainer{}
	}
	if request.Container.Linux.Devices == nil {
		request.Container.Linux.Devices = []*LinuxDevice{}
	}
	if request.Container.Linux.Resources == nil {
		request.Container.Linux.Resources = &LinuxResources{}
	}
	if request.Container.Linux.Resources.Memory == nil {
		request.Container.Linux.Resources.Memory = &LinuxMemory{}
	}
	if request.Container.Linux.Resources.Cpu == nil {
		request.Container.Linux.Resources.Cpu = &LinuxCPU{}
	}
	if request.Container.Linux.Resources.Unified == nil {
		request.Container.Linux.Resources.Unified = map[string]string{}
	}

	return &result{
		request: resultRequest{
			create: request,
		},
		reply: resultReply{
			adjust: &ContainerAdjustment{
				Annotations: map[string]string{},
				Mounts:      []*Mount{},
				Env:         []*KeyValue{},
				Hooks:       &Hooks{},
				Rlimits:     []*POSIXRlimit{},
				CDIDevices:  []*CDIDevice{},
				Linux: &LinuxContainerAdjustment{
					Devices: []*LinuxDevice{},
					Resources: &LinuxResources{
						Memory:         &LinuxMemory{},
						Cpu:            &LinuxCPU{},
						HugepageLimits: []*HugepageLimit{},
						Unified:        map[string]string{},
					},
				},
			},
		},
		updates: map[string]*ContainerUpdate{},
		owners:  resultOwners{},
	}
}

func collectUpdateContainerResult(request *UpdateContainerRequest) *result {
	if request != nil {
		if request.LinuxResources == nil {
			request.LinuxResources = &LinuxResources{}
		}
		if request.LinuxResources.Memory == nil {
			request.LinuxResources.Memory = &LinuxMemory{}
		}
		if request.LinuxResources.Cpu == nil {
			request.LinuxResources.Cpu = &LinuxCPU{}
		}
	}

	return &result{
		request: resultRequest{
			update: request,
		},
		reply: resultReply{
			update: []*ContainerUpdate{},
		},
		updates: map[string]*ContainerUpdate{},
		owners:  resultOwners{},
	}
}

func collectStopContainerResult() *result {
	return collectUpdateContainerResult(nil)
}

func (r *result) createContainerResponse() *CreateContainerResponse {
	return &CreateContainerResponse{
		Adjust: r.reply.adjust,
		Update: r.reply.update,
	}
}

func (r *result) updateContainerResponse() *UpdateContainerResponse {
	requested := r.updates[r.request.update.Container.Id]
	return &UpdateContainerResponse{
		Update: append(r.reply.update, requested),
	}
}

func (r *result) stopContainerResponse() *StopContainerResponse {
	return &StopContainerResponse{
		Update: r.reply.update,
	}
}

func (r *result) apply(response interface{}, plugin string) error {
	switch rpl := response.(type) {
	case *CreateContainerResponse:
		if rpl == nil {
			return nil
		}
		if err := r.adjust(rpl.Adjust, plugin); err != nil {
			return err
		}
		if err := r.update(rpl.Update, plugin); err != nil {
			return err
		}
	case *UpdateContainerResponse:
		if rpl == nil {
			return nil
		}
		if err := r.update(rpl.Update, plugin); err != nil {
			return err
		}
	case *StopContainerResponse:
		if rpl == nil {
			return nil
		}
		if err := r.update(rpl.Update, plugin); err != nil {
			return err
		}
	default:
		return fmt.Errorf("cannot apply response of invalid type %T", response)
	}

	return nil
}

func (r *result) adjust(rpl *ContainerAdjustment, plugin string) error {
	if rpl == nil {
		return nil
	}
	if err := r.adjustAnnotations(rpl.Annotations, plugin); err != nil {
		return err
	}
	if err := r.adjustMounts(rpl.Mounts, plugin); err != nil {
		return err
	}
	if err := r.adjustEnv(rpl.Env, plugin); err != nil {
		return err
	}
	if err := r.adjustHooks(rpl.Hooks); err != nil {
		return err
	}
	if rpl.Linux != nil {
		if err := r.adjustDevices(rpl.Linux.Devices, plugin); err != nil {
			return err
		}
		if err := r.adjustResources(rpl.Linux.Resources, plugin); err != nil {
			return err
		}
		if err := r.adjustCgroupsPath(rpl.Linux.CgroupsPath, plugin); err != nil {
			return err
		}
		if err := r.adjustOomScoreAdj(rpl.Linux.OomScoreAdj, plugin); err != nil {
			return err
		}
	}
	if err := r.adjustRlimits(rpl.Rlimits, plugin); err != nil {
		return err
	}
	if err := r.adjustCDIDevices(rpl.CDIDevices, plugin); err != nil {
		return err
	}

	return nil
}

func (r *result) update(updates []*ContainerUpdate, plugin string) error {
	for _, u := range updates {
		reply, err := r.getContainerUpdate(u, plugin)
		if err != nil {
			return err
		}
		if err := r.updateResources(reply, u, plugin); err != nil && !u.IgnoreFailure {
			return err
		}
	}

	return nil
}

func (r *result) adjustAnnotations(annotations map[string]string, plugin string) error {
	if len(annotations) == 0 {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id
	del := map[string]struct{}{}
	for k := range annotations {
		if key, marked := IsMarkedForRemoval(k); marked {
			del[key] = struct{}{}
			delete(annotations, k)
		}
	}

	for k, v := range annotations {
		if _, ok := del[k]; ok {
			r.owners.clearAnnotation(id, k)
			delete(create.Container.Annotations, k)
			r.reply.adjust.Annotations[MarkForRemoval(k)] = ""
		}
		if err := r.owners.claimAnnotation(id, k, plugin); err != nil {
			return err
		}
		create.Container.Annotations[k] = v
		r.reply.adjust.Annotations[k] = v
		delete(del, k)
	}

	for k := range del {
		r.reply.adjust.Annotations[MarkForRemoval(k)] = ""
	}

	return nil
}

func (r *result) adjustMounts(mounts []*Mount, plugin string) error {
	if len(mounts) == 0 {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id

	// first split removals from the rest of adjustments
	add := []*Mount{}
	del := map[string]*Mount{}
	mod := map[string]*Mount{}
	for _, m := range mounts {
		if key, marked := m.IsMarkedForRemoval(); marked {
			del[key] = m
		} else {
			add = append(add, m)
			mod[key] = m
		}
	}

	// next remove marked mounts from collected adjustments
	cleared := []*Mount{}
	for _, m := range r.reply.adjust.Mounts {
		if _, removed := del[m.Destination]; removed {
			r.owners.clearMount(id, m.Destination)
			continue
		}
		cleared = append(cleared, m)
	}
	r.reply.adjust.Mounts = cleared

	// next remove marked and modified mounts from container creation request
	cleared = []*Mount{}
	for _, m := range create.Container.Mounts {
		if _, removed := del[m.Destination]; removed {
			continue
		}
		if _, modified := mod[m.Destination]; modified {
			continue
		}
		cleared = append(cleared, m)
	}
	create.Container.Mounts = cleared

	// next, apply additions/modifications to collected adjustments
	for _, m := range add {
		if err := r.owners.claimMount(id, m.Destination, plugin); err != nil {
			return err
		}
		r.reply.adjust.Mounts = append(r.reply.adjust.Mounts, m)
	}

	// next, apply deletions with no corresponding additions
	for _, m := range del {
		if _, ok := mod[api.ClearRemovalMarker(m.Destination)]; !ok {
			r.reply.adjust.Mounts = append(r.reply.adjust.Mounts, m)
		}
	}

	// finally, apply additions/modifications to plugin container creation request
	create.Container.Mounts = append(create.Container.Mounts, add...)

	return nil
}

func (r *result) adjustDevices(devices []*LinuxDevice, plugin string) error {
	if len(devices) == 0 {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id

	// first split removals from the rest of adjustments
	add := []*LinuxDevice{}
	del := map[string]*LinuxDevice{}
	mod := map[string]*LinuxDevice{}
	for _, d := range devices {
		if key, marked := d.IsMarkedForRemoval(); marked {
			del[key] = d
		} else {
			add = append(add, d)
			mod[key] = d
		}
	}

	// next remove marked devices from collected adjustments
	cleared := []*LinuxDevice{}
	for _, d := range r.reply.adjust.Linux.Devices {
		if _, removed := del[d.Path]; removed {
			r.owners.clearDevice(id, d.Path)
			continue
		}
		cleared = append(cleared, d)
	}
	r.reply.adjust.Linux.Devices = cleared

	// next remove marked and modified devices from container creation request
	cleared = []*LinuxDevice{}
	for _, d := range create.Container.Linux.Devices {
		if _, removed := del[d.Path]; removed {
			continue
		}
		if _, modified := mod[d.Path]; modified {
			continue
		}
		cleared = append(cleared, d)
	}
	create.Container.Linux.Devices = cleared

	// next, apply additions/modifications to collected adjustments
	for _, d := range add {
		if err := r.owners.claimDevice(id, d.Path, plugin); err != nil {
			return err
		}
		r.reply.adjust.Linux.Devices = append(r.reply.adjust.Linux.Devices, d)
	}

	// finally, apply additions/modifications to plugin container creation request
	create.Container.Linux.Devices = append(create.Container.Linux.Devices, add...)

	return nil
}

func (r *result) adjustCDIDevices(devices []*CDIDevice, plugin string) error {
	if len(devices) == 0 {
		return nil
	}

	// Notes:
	//   CDI devices are opaque references, typically to vendor specific
	//   devices. They get resolved to actual devices and potential related
	//   mounts, environment variables, etc. in the runtime. Unlike with
	//   devices, we only allow CDI device references to be added to a
	//   container, not removed. We pass them unresolved to the runtime and
	//   have them resolved there. Also unlike with devices, we don't include
	//   CDI device references in creation requests. However, since there
	//   is typically a strong ownership and a single related management entity
	//   per vendor/class for these devices we do treat multiple injection of
	//   the same CDI device reference as an error here.

	id := r.request.create.Container.Id

	// apply additions to collected adjustments
	for _, d := range devices {
		if err := r.owners.claimCDIDevice(id, d.Name, plugin); err != nil {
			return err
		}
		r.reply.adjust.CDIDevices = append(r.reply.adjust.CDIDevices, d)
	}

	return nil
}

func (r *result) adjustEnv(env []*KeyValue, plugin string) error {
	if len(env) == 0 {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id

	// first split removals from the rest of adjustments
	add := []*KeyValue{}
	del := map[string]struct{}{}
	mod := map[string]struct{}{}
	for _, e := range env {
		if key, marked := e.IsMarkedForRemoval(); marked {
			del[key] = struct{}{}
		} else {
			add = append(add, e)
			mod[key] = struct{}{}
		}
	}

	// next remove marked environment variables from collected adjustments
	cleared := []*KeyValue{}
	for _, e := range r.reply.adjust.Env {
		if _, removed := del[e.Key]; removed {
			r.owners.clearEnv(id, e.Key)
			continue
		}
		cleared = append(cleared, e)
	}
	r.reply.adjust.Env = cleared

	// next remove marked and modified environment from container creation request
	clearedEnv := []string{}
	for _, e := range create.Container.Env {
		key, _ := splitEnvVar(e)
		if _, removed := del[key]; removed {
			continue
		}
		if _, modified := mod[key]; modified {
			continue
		}
		clearedEnv = append(clearedEnv, e)
	}
	create.Container.Env = clearedEnv

	// next, apply additions/modifications to collected adjustments
	for _, e := range add {
		if err := r.owners.claimEnv(id, e.Key, plugin); err != nil {
			return err
		}
		r.reply.adjust.Env = append(r.reply.adjust.Env, e)
	}

	// finally, apply additions/modifications to plugin container creation request
	for _, e := range add {
		create.Container.Env = append(create.Container.Env, e.ToOCI())
	}

	return nil
}

func splitEnvVar(s string) (string, string) {
	split := strings.SplitN(s, "=", 2)
	if len(split) < 1 {
		return "", ""
	}
	if len(split) != 2 {
		return split[0], ""
	}
	return split[0], split[1]
}

func (r *result) adjustHooks(hooks *Hooks) error {
	if hooks == nil {
		return nil
	}

	reply := r.reply.adjust
	container := r.request.create.Container

	if h := hooks.Prestart; len(h) > 0 {
		reply.Hooks.Prestart = append(reply.Hooks.Prestart, h...)
		container.Hooks.Prestart = append(container.Hooks.Prestart, h...)
	}
	if h := hooks.Poststart; len(h) > 0 {
		reply.Hooks.Poststart = append(reply.Hooks.Poststart, h...)
		container.Hooks.Poststart = append(container.Hooks.Poststart, h...)
	}
	if h := hooks.Poststop; len(h) > 0 {
		reply.Hooks.Poststop = append(reply.Hooks.Poststop, h...)
		container.Hooks.Poststop = append(container.Hooks.Poststop, h...)
	}
	if h := hooks.CreateRuntime; len(h) > 0 {
		reply.Hooks.CreateRuntime = append(reply.Hooks.CreateRuntime, h...)
		container.Hooks.CreateRuntime = append(container.Hooks.CreateRuntime, h...)
	}
	if h := hooks.CreateContainer; len(h) > 0 {
		reply.Hooks.CreateContainer = append(reply.Hooks.CreateContainer, h...)
		container.Hooks.CreateContainer = append(container.Hooks.CreateContainer, h...)
	}
	if h := hooks.StartContainer; len(h) > 0 {
		reply.Hooks.StartContainer = append(reply.Hooks.StartContainer, h...)
		container.Hooks.StartContainer = append(container.Hooks.StartContainer, h...)
	}

	return nil
}

func (r *result) adjustResources(resources *LinuxResources, plugin string) error {
	if resources == nil {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id
	container := create.Container.Linux.Resources
	reply := r.reply.adjust.Linux.Resources

	if mem := resources.Memory; mem != nil {
		if v := mem.GetLimit(); v != nil {
			if err := r.owners.claimMemLimit(id, plugin); err != nil {
				return err
			}
			container.Memory.Limit = Int64(v.GetValue())
			reply.Memory.Limit = Int64(v.GetValue())
		}
		if v := mem.GetReservation(); v != nil {
			if err := r.owners.claimMemReservation(id, plugin); err != nil {
				return err
			}
			container.Memory.Reservation = Int64(v.GetValue())
			reply.Memory.Reservation = Int64(v.GetValue())
		}
		if v := mem.GetSwap(); v != nil {
			if err := r.owners.claimMemSwapLimit(id, plugin); err != nil {
				return err
			}
			container.Memory.Swap = Int64(v.GetValue())
			reply.Memory.Swap = Int64(v.GetValue())
		}
		if v := mem.GetKernel(); v != nil {
			if err := r.owners.claimMemKernelLimit(id, plugin); err != nil {
				return err
			}
			container.Memory.Kernel = Int64(v.GetValue())
			reply.Memory.Kernel = Int64(v.GetValue())
		}
		if v := mem.GetKernelTcp(); v != nil {
			if err := r.owners.claimMemTCPLimit(id, plugin); err != nil {
				return err
			}
			container.Memory.KernelTcp = Int64(v.GetValue())
			reply.Memory.KernelTcp = Int64(v.GetValue())
		}
		if v := mem.GetSwappiness(); v != nil {
			if err := r.owners.claimMemSwappiness(id, plugin); err != nil {
				return err
			}
			container.Memory.Swappiness = UInt64(v.GetValue())
			reply.Memory.Swappiness = UInt64(v.GetValue())
		}
		if v := mem.GetDisableOomKiller(); v != nil {
			if err := r.owners.claimMemDisableOomKiller(id, plugin); err != nil {
				return err
			}
			container.Memory.DisableOomKiller = Bool(v.GetValue())
			reply.Memory.DisableOomKiller = Bool(v.GetValue())
		}
		if v := mem.GetUseHierarchy(); v != nil {
			if err := r.owners.claimMemUseHierarchy(id, plugin); err != nil {
				return err
			}
			container.Memory.UseHierarchy = Bool(v.GetValue())
			reply.Memory.UseHierarchy = Bool(v.GetValue())
		}
	}
	if cpu := resources.Cpu; cpu != nil {
		if v := cpu.GetShares(); v != nil {
			if err := r.owners.claimCpuShares(id, plugin); err != nil {
				return err
			}
			container.Cpu.Shares = UInt64(v.GetValue())
			reply.Cpu.Shares = UInt64(v.GetValue())
		}
		if v := cpu.GetQuota(); v != nil {
			if err := r.owners.claimCpuQuota(id, plugin); err != nil {
				return err
			}
			container.Cpu.Quota = Int64(v.GetValue())
			reply.Cpu.Quota = Int64(v.GetValue())
		}
		if v := cpu.GetPeriod(); v != nil {
			if err := r.owners.claimCpuPeriod(id, plugin); err != nil {
				return err
			}
			container.Cpu.Period = UInt64(v.GetValue())
			reply.Cpu.Period = UInt64(v.GetValue())
		}
		if v := cpu.GetRealtimeRuntime(); v != nil {
			if err := r.owners.claimCpuRealtimeRuntime(id, plugin); err != nil {
				return err
			}
			container.Cpu.RealtimeRuntime = Int64(v.GetValue())
			reply.Cpu.RealtimeRuntime = Int64(v.GetValue())
		}
		if v := cpu.GetRealtimePeriod(); v != nil {
			if err := r.owners.claimCpuRealtimePeriod(id, plugin); err != nil {
				return err
			}
			container.Cpu.RealtimePeriod = UInt64(v.GetValue())
			reply.Cpu.RealtimePeriod = UInt64(v.GetValue())
		}
		if v := cpu.GetCpus(); v != "" {
			if err := r.owners.claimCpusetCpus(id, plugin); err != nil {
				return err
			}
			container.Cpu.Cpus = v
			reply.Cpu.Cpus = v
		}
		if v := cpu.GetMems(); v != "" {
			if err := r.owners.claimCpusetMems(id, plugin); err != nil {
				return err
			}
			container.Cpu.Mems = v
			reply.Cpu.Mems = v
		}
	}

	for _, l := range resources.HugepageLimits {
		if err := r.owners.claimHugepageLimit(id, l.PageSize, plugin); err != nil {
			return err
		}
		container.HugepageLimits = append(container.HugepageLimits, l)
		reply.HugepageLimits = append(reply.HugepageLimits, l)
	}

	if len(resources.Unified) != 0 {
		for k, v := range resources.Unified {
			if err := r.owners.claimUnified(id, k, plugin); err != nil {
				return err
			}
			container.Unified[k] = v
			reply.Unified[k] = v
		}
	}

	if v := resources.GetBlockioClass(); v != nil {
		if err := r.owners.claimBlockioClass(id, plugin); err != nil {
			return err
		}
		container.BlockioClass = String(v.GetValue())
		reply.BlockioClass = String(v.GetValue())
	}
	if v := resources.GetRdtClass(); v != nil {
		if err := r.owners.claimRdtClass(id, plugin); err != nil {
			return err
		}
		container.RdtClass = String(v.GetValue())
		reply.RdtClass = String(v.GetValue())
	}
	if v := resources.GetPids(); v != nil {
		if err := r.owners.claimPidsLimit(id, plugin); err != nil {
			return err
		}
		pidv := &api.LinuxPids{
			Limit: v.GetLimit(),
		}
		container.Pids = pidv
		reply.Pids = pidv
	}
	return nil
}

func (r *result) adjustCgroupsPath(path, plugin string) error {
	if path == "" {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id

	if err := r.owners.claimCgroupsPath(id, plugin); err != nil {
		return err
	}

	create.Container.Linux.CgroupsPath = path
	r.reply.adjust.Linux.CgroupsPath = path

	return nil
}

func (r *result) adjustOomScoreAdj(OomScoreAdj *OptionalInt, plugin string) error {
	if OomScoreAdj == nil {
		return nil
	}

	create, id := r.request.create, r.request.create.Container.Id

	if err := r.owners.claimOomScoreAdj(id, plugin); err != nil {
		return err
	}

	create.Container.Linux.OomScoreAdj = OomScoreAdj
	r.reply.adjust.Linux.OomScoreAdj = OomScoreAdj

	return nil
}

func (r *result) adjustRlimits(rlimits []*POSIXRlimit, plugin string) error {
	create, id, adjust := r.request.create, r.request.create.Container.Id, r.reply.adjust
	for _, l := range rlimits {
		if err := r.owners.claimRlimits(id, l.Type, plugin); err != nil {
			return err
		}

		create.Container.Rlimits = append(create.Container.Rlimits, l)
		adjust.Rlimits = append(adjust.Rlimits, l)
	}
	return nil
}

func (r *result) updateResources(reply, u *ContainerUpdate, plugin string) error {
	if u.Linux == nil || u.Linux.Resources == nil {
		return nil
	}

	var resources *LinuxResources
	request, id := r.request.update, u.ContainerId

	// operate on a copy: we won't touch anything on (ignored) failures
	if request != nil && request.Container.Id == id {
		resources = request.LinuxResources.Copy()
	} else {
		resources = reply.Linux.Resources.Copy()
	}

	if mem := u.Linux.Resources.Memory; mem != nil {
		if v := mem.GetLimit(); v != nil {
			if err := r.owners.claimMemLimit(id, plugin); err != nil {
				return err
			}
			resources.Memory.Limit = Int64(v.GetValue())
		}
		if v := mem.GetReservation(); v != nil {
			if err := r.owners.claimMemReservation(id, plugin); err != nil {
				return err
			}
			resources.Memory.Reservation = Int64(v.GetValue())
		}
		if v := mem.GetSwap(); v != nil {
			if err := r.owners.claimMemSwapLimit(id, plugin); err != nil {
				return err
			}
			resources.Memory.Swap = Int64(v.GetValue())
		}
		if v := mem.GetKernel(); v != nil {
			if err := r.owners.claimMemKernelLimit(id, plugin); err != nil {
				return err
			}
			resources.Memory.Kernel = Int64(v.GetValue())
		}
		if v := mem.GetKernelTcp(); v != nil {
			if err := r.owners.claimMemTCPLimit(id, plugin); err != nil {
				return err
			}
			resources.Memory.KernelTcp = Int64(v.GetValue())
		}
		if v := mem.GetSwappiness(); v != nil {
			if err := r.owners.claimMemSwappiness(id, plugin); err != nil {
				return err
			}
			resources.Memory.Swappiness = UInt64(v.GetValue())
		}
		if v := mem.GetDisableOomKiller(); v != nil {
			if err := r.owners.claimMemDisableOomKiller(id, plugin); err != nil {
				return err
			}
			resources.Memory.DisableOomKiller = Bool(v.GetValue())
		}
		if v := mem.GetUseHierarchy(); v != nil {
			if err := r.owners.claimMemUseHierarchy(id, plugin); err != nil {
				return err
			}
			resources.Memory.UseHierarchy = Bool(v.GetValue())
		}
	}
	if cpu := u.Linux.Resources.Cpu; cpu != nil {
		if v := cpu.GetShares(); v != nil {
			if err := r.owners.claimCpuShares(id, plugin); err != nil {
				return err
			}
			resources.Cpu.Shares = UInt64(v.GetValue())
		}
		if v := cpu.GetQuota(); v != nil {
			if err := r.owners.claimCpuQuota(id, plugin); err != nil {
				return err
			}
			resources.Cpu.Quota = Int64(v.GetValue())
		}
		if v := cpu.GetPeriod(); v != nil {
			if err := r.owners.claimCpuPeriod(id, plugin); err != nil {
				return err
			}
			resources.Cpu.Period = UInt64(v.GetValue())
		}
		if v := cpu.GetRealtimeRuntime(); v != nil {
			if err := r.owners.claimCpuRealtimeRuntime(id, plugin); err != nil {
				return err
			}
			resources.Cpu.RealtimeRuntime = Int64(v.GetValue())
		}
		if v := cpu.GetRealtimePeriod(); v != nil {
			if err := r.owners.claimCpuRealtimePeriod(id, plugin); err != nil {
				return err
			}
			resources.Cpu.RealtimePeriod = UInt64(v.GetValue())
		}
		if v := cpu.GetCpus(); v != "" {
			if err := r.owners.claimCpusetCpus(id, plugin); err != nil {
				return err
			}
			resources.Cpu.Cpus = v
		}
		if v := cpu.GetMems(); v != "" {
			if err := r.owners.claimCpusetMems(id, plugin); err != nil {
				return err
			}
			resources.Cpu.Mems = v
		}
	}

	for _, l := range u.Linux.Resources.HugepageLimits {
		if err := r.owners.claimHugepageLimit(id, l.PageSize, plugin); err != nil {
			return err
		}
		resources.HugepageLimits = append(resources.HugepageLimits, l)
	}

	if len(u.Linux.Resources.Unified) != 0 {
		if resources.Unified == nil {
			resources.Unified = make(map[string]string)
		}
		for k, v := range u.Linux.Resources.Unified {
			if err := r.owners.claimUnified(id, k, plugin); err != nil {
				return err
			}
			resources.Unified[k] = v
		}
	}

	if v := u.Linux.Resources.GetBlockioClass(); v != nil {
		if err := r.owners.claimBlockioClass(id, plugin); err != nil {
			return err
		}
		resources.BlockioClass = String(v.GetValue())
	}
	if v := u.Linux.Resources.GetRdtClass(); v != nil {
		if err := r.owners.claimRdtClass(id, plugin); err != nil {
			return err
		}
		resources.RdtClass = String(v.GetValue())
	}
	if v := resources.GetPids(); v != nil {
		if err := r.owners.claimPidsLimit(id, plugin); err != nil {
			return err
		}
		resources.Pids = &api.LinuxPids{
			Limit: v.GetLimit(),
		}
	}

	// update request/reply from copy on success
	reply.Linux.Resources = resources.Copy()

	if request != nil && request.Container.Id == id {
		request.LinuxResources = resources.Copy()
	}

	return nil
}

func (r *result) getContainerUpdate(u *ContainerUpdate, plugin string) (*ContainerUpdate, error) {
	id := u.ContainerId
	if r.request.create != nil && r.request.create.Container != nil {
		if r.request.create.Container.Id == id {
			return nil, fmt.Errorf("plugin %q asked update of %q during creation",
				plugin, id)
		}
	}

	if update, ok := r.updates[id]; ok {
		update.IgnoreFailure = update.IgnoreFailure && u.IgnoreFailure
		return update, nil
	}

	update := &ContainerUpdate{
		ContainerId: id,
		Linux: &LinuxContainerUpdate{
			Resources: &LinuxResources{
				Memory:         &LinuxMemory{},
				Cpu:            &LinuxCPU{},
				HugepageLimits: []*HugepageLimit{},
				Unified:        map[string]string{},
			},
		},
		IgnoreFailure: u.IgnoreFailure,
	}

	r.updates[id] = update

	// for update requests delay appending the requested container (in the response getter)
	if r.request.update == nil || r.request.update.Container.Id != id {
		r.reply.update = append(r.reply.update, update)
	}

	return update, nil
}

type owners struct {
	annotations         map[string]string
	mounts              map[string]string
	devices             map[string]string
	cdiDevices          map[string]string
	env                 map[string]string
	memLimit            string
	memReservation      string
	memSwapLimit        string
	memKernelLimit      string
	memTCPLimit         string
	memSwappiness       string
	memDisableOomKiller string
	memUseHierarchy     string
	cpuShares           string
	cpuQuota            string
	cpuPeriod           string
	cpuRealtimeRuntime  string
	cpuRealtimePeriod   string
	cpusetCpus          string
	cpusetMems          string
	pidsLimit           string
	hugepageLimits      map[string]string
	blockioClass        string
	rdtClass            string
	unified             map[string]string
	cgroupsPath         string
	oomScoreAdj         string
	rlimits             map[string]string
}

func (ro resultOwners) ownersFor(id string) *owners {
	o, ok := ro[id]
	if !ok {
		o = &owners{}
		ro[id] = o
	}
	return o
}

func (ro resultOwners) claimAnnotation(id, key, plugin string) error {
	return ro.ownersFor(id).claimAnnotation(key, plugin)
}

func (ro resultOwners) claimMount(id, destination, plugin string) error {
	return ro.ownersFor(id).claimMount(destination, plugin)
}

func (ro resultOwners) claimDevice(id, path, plugin string) error {
	return ro.ownersFor(id).claimDevice(path, plugin)
}

func (ro resultOwners) claimCDIDevice(id, path, plugin string) error {
	return ro.ownersFor(id).claimCDIDevice(path, plugin)
}

func (ro resultOwners) claimEnv(id, name, plugin string) error {
	return ro.ownersFor(id).claimEnv(name, plugin)
}

func (ro resultOwners) claimMemLimit(id, plugin string) error {
	return ro.ownersFor(id).claimMemLimit(plugin)
}

func (ro resultOwners) claimMemReservation(id, plugin string) error {
	return ro.ownersFor(id).claimMemReservation(plugin)
}

func (ro resultOwners) claimMemSwapLimit(id, plugin string) error {
	return ro.ownersFor(id).claimMemSwapLimit(plugin)
}

func (ro resultOwners) claimMemKernelLimit(id, plugin string) error {
	return ro.ownersFor(id).claimMemKernelLimit(plugin)
}

func (ro resultOwners) claimMemTCPLimit(id, plugin string) error {
	return ro.ownersFor(id).claimMemTCPLimit(plugin)
}

func (ro resultOwners) claimMemSwappiness(id, plugin string) error {
	return ro.ownersFor(id).claimMemSwappiness(plugin)
}

func (ro resultOwners) claimMemDisableOomKiller(id, plugin string) error {
	return ro.ownersFor(id).claimMemDisableOomKiller(plugin)
}

func (ro resultOwners) claimMemUseHierarchy(id, plugin string) error {
	return ro.ownersFor(id).claimMemUseHierarchy(plugin)
}

func (ro resultOwners) claimCpuShares(id, plugin string) error {
	return ro.ownersFor(id).claimCpuShares(plugin)
}

func (ro resultOwners) claimCpuQuota(id, plugin string) error {
	return ro.ownersFor(id).claimCpuQuota(plugin)
}

func (ro resultOwners) claimCpuPeriod(id, plugin string) error {
	return ro.ownersFor(id).claimCpuPeriod(plugin)
}

func (ro resultOwners) claimCpuRealtimeRuntime(id, plugin string) error {
	return ro.ownersFor(id).claimCpuRealtimeRuntime(plugin)
}

func (ro resultOwners) claimCpuRealtimePeriod(id, plugin string) error {
	return ro.ownersFor(id).claimCpuRealtimePeriod(plugin)
}

func (ro resultOwners) claimCpusetCpus(id, plugin string) error {
	return ro.ownersFor(id).claimCpusetCpus(plugin)
}

func (ro resultOwners) claimCpusetMems(id, plugin string) error {
	return ro.ownersFor(id).claimCpusetMems(plugin)
}

func (ro resultOwners) claimPidsLimit(id, plugin string) error {
	return ro.ownersFor(id).claimPidsLimit(plugin)
}

func (ro resultOwners) claimHugepageLimit(id, size, plugin string) error {
	return ro.ownersFor(id).claimHugepageLimit(size, plugin)
}

func (ro resultOwners) claimBlockioClass(id, plugin string) error {
	return ro.ownersFor(id).claimBlockioClass(plugin)
}

func (ro resultOwners) claimRdtClass(id, plugin string) error {
	return ro.ownersFor(id).claimRdtClass(plugin)
}

func (ro resultOwners) claimUnified(id, key, plugin string) error {
	return ro.ownersFor(id).claimUnified(key, plugin)
}

func (ro resultOwners) claimCgroupsPath(id, plugin string) error {
	return ro.ownersFor(id).claimCgroupsPath(plugin)
}

func (ro resultOwners) claimOomScoreAdj(id, plugin string) error {
	return ro.ownersFor(id).claimOomScoreAdj(plugin)
}

func (ro resultOwners) claimRlimits(id, typ, plugin string) error {
	return ro.ownersFor(id).claimRlimit(typ, plugin)
}

func (o *owners) claimAnnotation(key, plugin string) error {
	if o.annotations == nil {
		o.annotations = make(map[string]string)
	}
	if other, taken := o.annotations[key]; taken {
		return conflict(plugin, other, "annotation", key)
	}
	o.annotations[key] = plugin
	return nil
}

func (o *owners) claimMount(destination, plugin string) error {
	if o.mounts == nil {
		o.mounts = make(map[string]string)
	}
	if other, taken := o.mounts[destination]; taken {
		return conflict(plugin, other, "mount", destination)
	}
	o.mounts[destination] = plugin
	return nil
}

func (o *owners) claimDevice(path, plugin string) error {
	if o.devices == nil {
		o.devices = make(map[string]string)
	}
	if other, taken := o.devices[path]; taken {
		return conflict(plugin, other, "device", path)
	}
	o.devices[path] = plugin
	return nil
}

func (o *owners) claimCDIDevice(name, plugin string) error {
	if o.cdiDevices == nil {
		o.cdiDevices = make(map[string]string)
	}
	if other, taken := o.cdiDevices[name]; taken {
		return conflict(plugin, other, "CDI device", name)
	}
	o.cdiDevices[name] = plugin
	return nil
}

func (o *owners) claimEnv(name, plugin string) error {
	if o.env == nil {
		o.env = make(map[string]string)
	}
	if other, taken := o.env[name]; taken {
		return conflict(plugin, other, "env", name)
	}
	o.env[name] = plugin
	return nil
}

func (o *owners) claimMemLimit(plugin string) error {
	if other := o.memLimit; other != "" {
		return conflict(plugin, other, "memory limit")
	}
	o.memLimit = plugin
	return nil
}

func (o *owners) claimMemReservation(plugin string) error {
	if other := o.memReservation; other != "" {
		return conflict(plugin, other, "memory reservation")
	}
	o.memReservation = plugin
	return nil
}

func (o *owners) claimMemSwapLimit(plugin string) error {
	if other := o.memSwapLimit; other != "" {
		return conflict(plugin, other, "memory swap limit")
	}
	o.memSwapLimit = plugin
	return nil
}

func (o *owners) claimMemKernelLimit(plugin string) error {
	if other := o.memKernelLimit; other != "" {
		return conflict(plugin, other, "memory kernel limit")
	}
	o.memKernelLimit = plugin
	return nil
}

func (o *owners) claimMemTCPLimit(plugin string) error {
	if other := o.memTCPLimit; other != "" {
		return conflict(plugin, other, "memory TCP limit")
	}
	o.memTCPLimit = plugin
	return nil
}

func (o *owners) claimMemSwappiness(plugin string) error {
	if other := o.memSwappiness; other != "" {
		return conflict(plugin, other, "memory swappiness")
	}
	o.memSwappiness = plugin
	return nil
}

func (o *owners) claimMemDisableOomKiller(plugin string) error {
	if other := o.memDisableOomKiller; other != "" {
		return conflict(plugin, other, "memory disable OOM killer")
	}
	o.memDisableOomKiller = plugin
	return nil
}

func (o *owners) claimMemUseHierarchy(plugin string) error {
	if other := o.memUseHierarchy; other != "" {
		return conflict(plugin, other, "memory 'UseHierarchy'")
	}
	o.memUseHierarchy = plugin
	return nil
}

func (o *owners) claimCpuShares(plugin string) error {
	if other := o.cpuShares; other != "" {
		return conflict(plugin, other, "CPU shares")
	}
	o.cpuShares = plugin
	return nil
}

func (o *owners) claimCpuQuota(plugin string) error {
	if other := o.cpuQuota; other != "" {
		return conflict(plugin, other, "CPU quota")
	}
	o.cpuQuota = plugin
	return nil
}

func (o *owners) claimCpuPeriod(plugin string) error {
	if other := o.cpuPeriod; other != "" {
		return conflict(plugin, other, "CPU period")
	}
	o.cpuPeriod = plugin
	return nil
}

func (o *owners) claimCpuRealtimeRuntime(plugin string) error {
	if other := o.cpuRealtimeRuntime; other != "" {
		return conflict(plugin, other, "CPU realtime runtime")
	}
	o.cpuRealtimeRuntime = plugin
	return nil
}

func (o *owners) claimCpuRealtimePeriod(plugin string) error {
	if other := o.cpuRealtimePeriod; other != "" {
		return conflict(plugin, other, "CPU realtime period")
	}
	o.cpuRealtimePeriod = plugin
	return nil
}

func (o *owners) claimCpusetCpus(plugin string) error {
	if other := o.cpusetCpus; other != "" {
		return conflict(plugin, other, "CPU pinning")
	}
	o.cpusetCpus = plugin
	return nil
}

func (o *owners) claimCpusetMems(plugin string) error {
	if other := o.cpusetMems; other != "" {
		return conflict(plugin, other, "memory pinning")
	}
	o.cpusetMems = plugin
	return nil
}

func (o *owners) claimPidsLimit(plugin string) error {
	if other := o.pidsLimit; other != "" {
		return conflict(plugin, other, "pids pinning")
	}
	o.pidsLimit = plugin
	return nil
}

func (o *owners) claimHugepageLimit(size, plugin string) error {
	if o.hugepageLimits == nil {
		o.hugepageLimits = make(map[string]string)
	}

	if other, taken := o.hugepageLimits[size]; taken {
		return conflict(plugin, other, "hugepage limit of size", size)
	}
	o.hugepageLimits[size] = plugin
	return nil
}

func (o *owners) claimBlockioClass(plugin string) error {
	if other := o.blockioClass; other != "" {
		return conflict(plugin, other, "block I/O class")
	}
	o.blockioClass = plugin
	return nil
}

func (o *owners) claimRdtClass(plugin string) error {
	if other := o.rdtClass; other != "" {
		return conflict(plugin, other, "RDT class")
	}
	o.rdtClass = plugin
	return nil
}

func (o *owners) claimUnified(key, plugin string) error {
	if o.unified == nil {
		o.unified = make(map[string]string)
	}
	if other, taken := o.unified[key]; taken {
		return conflict(plugin, other, "unified resource", key)
	}
	o.unified[key] = plugin
	return nil
}

func (o *owners) claimRlimit(typ, plugin string) error {
	if o.rlimits == nil {
		o.rlimits = make(map[string]string)
	}
	if other, taken := o.rlimits[typ]; taken {
		return conflict(plugin, other, "rlimit", typ)
	}
	o.rlimits[typ] = plugin
	return nil
}

func (o *owners) claimCgroupsPath(plugin string) error {
	if other := o.cgroupsPath; other != "" {
		return conflict(plugin, other, "cgroups path")
	}
	o.cgroupsPath = plugin
	return nil
}

func (o *owners) claimOomScoreAdj(plugin string) error {
	if other := o.oomScoreAdj; other != "" {
		return conflict(plugin, other, "oom score adj")
	}
	o.oomScoreAdj = plugin
	return nil
}

func (ro resultOwners) clearAnnotation(id, key string) {
	ro.ownersFor(id).clearAnnotation(key)
}

func (ro resultOwners) clearMount(id, destination string) {
	ro.ownersFor(id).clearMount(destination)
}

func (ro resultOwners) clearDevice(id, path string) {
	ro.ownersFor(id).clearDevice(path)
}

func (ro resultOwners) clearEnv(id, name string) {
	ro.ownersFor(id).clearEnv(name)
}

func (o *owners) clearAnnotation(key string) {
	if o.annotations == nil {
		return
	}
	delete(o.annotations, key)
}

func (o *owners) clearMount(destination string) {
	if o.mounts == nil {
		return
	}
	delete(o.mounts, destination)
}

func (o *owners) clearDevice(path string) {
	if o.devices == nil {
		return
	}
	delete(o.devices, path)
}

func (o *owners) clearEnv(name string) {
	if o.env == nil {
		return
	}
	delete(o.env, name)
}

func conflict(plugin, other, subject string, qualif ...string) error {
	return fmt.Errorf("plugins %q and %q both tried to set %s",
		plugin, other, strings.Join(append([]string{subject}, qualif...), " "))
}

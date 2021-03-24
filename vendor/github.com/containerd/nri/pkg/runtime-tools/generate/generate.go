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

package generate

import (
	"fmt"
	"strings"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"

	nri "github.com/containerd/nri/pkg/api"
)

// GeneratorOption is an option for Generator().
type GeneratorOption func(*Generator)

// Generator extends a stock runtime-tools Generator and extends it with
// a few functions for handling NRI container adjustment.
type Generator struct {
	*generate.Generator
	filterLabels      func(map[string]string) (map[string]string, error)
	filterAnnotations func(map[string]string) (map[string]string, error)
	resolveBlockIO    func(string) (*rspec.LinuxBlockIO, error)
	resolveRdt        func(string) (*rspec.LinuxIntelRdt, error)
	checkResources    func(*rspec.LinuxResources) error
}

// SpecGenerator returns a wrapped OCI Spec Generator.
func SpecGenerator(gg *generate.Generator, opts ...GeneratorOption) *Generator {
	g := &Generator{
		Generator: gg,
	}
	g.filterLabels = nopFilter
	g.filterAnnotations = nopFilter
	for _, o := range opts {
		o(g)
	}
	return g
}

// WithLabelFilter provides an option for filtering or rejecting labels.
func WithLabelFilter(fn func(map[string]string) (map[string]string, error)) GeneratorOption {
	return func(g *Generator) {
		g.filterLabels = fn
	}
}

// WithAnnotationFilter provides an option for filtering or rejecting annotations.
func WithAnnotationFilter(fn func(map[string]string) (map[string]string, error)) GeneratorOption {
	return func(g *Generator) {
		g.filterAnnotations = fn
	}
}

// WithBlockIOResolver specifies a function for resolving Block I/O classes by name.
func WithBlockIOResolver(fn func(string) (*rspec.LinuxBlockIO, error)) GeneratorOption {
	return func(g *Generator) {
		g.resolveBlockIO = fn
	}
}

// WithRdtResolver specifies a function for resolving RDT classes by name.
func WithRdtResolver(fn func(string) (*rspec.LinuxIntelRdt, error)) GeneratorOption {
	return func(g *Generator) {
		g.resolveRdt = fn
	}
}

// WithResourceChecker specifies a function to perform final resource adjustment.
func WithResourceChecker(fn func(*rspec.LinuxResources) error) GeneratorOption {
	return func(g *Generator) {
		g.checkResources = fn
	}
}

// Adjust adjusts all aspects of the OCI Spec that NRI knows/cares about.
func (g *Generator) Adjust(adjust *nri.ContainerAdjustment) error {
	if adjust == nil {
		return nil
	}

	if err := g.AdjustAnnotations(adjust.GetAnnotations()); err != nil {
		return fmt.Errorf("failed to adjust annotations in OCI Spec: %w", err)
	}
	g.AdjustEnv(adjust.GetEnv())
	g.AdjustHooks(adjust.GetHooks())
	g.AdjustDevices(adjust.GetLinux().GetDevices())
	g.AdjustCgroupsPath(adjust.GetLinux().GetCgroupsPath())

	resources := adjust.GetLinux().GetResources()
	if err := g.AdjustResources(resources); err != nil {
		return err
	}
	if err := g.AdjustBlockIOClass(resources.GetBlockioClass().Get()); err != nil {
		return err
	}
	if err := g.AdjustRdtClass(resources.GetRdtClass().Get()); err != nil {
		return err
	}

	if err := g.AdjustMounts(adjust.GetMounts()); err != nil {
		return err
	}

	return nil
}

// AdjustEnv adjusts the environment of the OCI Spec.
func (g *Generator) AdjustEnv(env []*nri.KeyValue) {
	mod := map[string]*nri.KeyValue{}

	for _, e := range env {
		key, _ := nri.IsMarkedForRemoval(e.Key)
		mod[key] = e
	}

	// first modify existing environment
	if len(mod) > 0 && g.Config != nil && g.Config.Process != nil {
		old := g.Config.Process.Env
		g.ClearProcessEnv()
		for _, e := range old {
			keyval := strings.SplitN(e, "=", 2)
			if len(keyval) < 2 {
				continue
			}
			if m, ok := mod[keyval[0]]; ok {
				delete(mod, keyval[0])
				if _, marked := m.IsMarkedForRemoval(); !marked {
					g.AddProcessEnv(m.Key, m.Value)
				}
				continue
			}
			g.AddProcessEnv(keyval[0], keyval[1])
		}
	}

	// then append remaining unprocessed adjustments (new variables)
	for _, e := range env {
		if _, marked := e.IsMarkedForRemoval(); marked {
			continue
		}
		if _, ok := mod[e.Key]; ok {
			g.AddProcessEnv(e.Key, e.Value)
		}
	}
}

// AdjustAnnotations adjusts the annotations in the OCI Spec.
func (g *Generator) AdjustAnnotations(annotations map[string]string) error {
	var err error

	if annotations, err = g.filterAnnotations(annotations); err != nil {
		return err
	}
	for k, v := range annotations {
		if key, marked := nri.IsMarkedForRemoval(k); marked {
			g.RemoveAnnotation(key)
		} else {
			g.AddAnnotation(k, v)
		}
	}

	return nil
}

// AdjustHooks adjusts the OCI hooks in the OCI Spec.
func (g *Generator) AdjustHooks(hooks *nri.Hooks) {
	if hooks == nil {
		return
	}
	for _, h := range hooks.Prestart {
		g.AddPreStartHook(h.ToOCI())
	}
	for _, h := range hooks.Poststart {
		g.AddPostStartHook(h.ToOCI())
	}
	for _, h := range hooks.Poststop {
		g.AddPostStopHook(h.ToOCI())
	}
	for _, h := range hooks.CreateRuntime {
		g.AddCreateRuntimeHook(h.ToOCI())
	}
	for _, h := range hooks.CreateContainer {
		g.AddCreateContainerHook(h.ToOCI())
	}
	for _, h := range hooks.StartContainer {
		g.AddStartContainerHook(h.ToOCI())
	}
}

// AdjustResources adjusts the (Linux) resources in the OCI Spec.
func (g *Generator) AdjustResources(r *nri.LinuxResources) error {
	if r == nil {
		return nil
	}

	g.initConfigLinux()

	if r.Cpu != nil {
		g.SetLinuxResourcesCPUPeriod(r.Cpu.GetPeriod().GetValue())
		g.SetLinuxResourcesCPUQuota(r.Cpu.GetQuota().GetValue())
		g.SetLinuxResourcesCPUShares(r.Cpu.GetShares().GetValue())
		g.SetLinuxResourcesCPUCpus(r.Cpu.GetCpus())
		g.SetLinuxResourcesCPUMems(r.Cpu.GetMems())
		g.SetLinuxResourcesCPURealtimeRuntime(r.Cpu.GetRealtimeRuntime().GetValue())
		g.SetLinuxResourcesCPURealtimePeriod(r.Cpu.GetRealtimePeriod().GetValue())
	}
	if r.Memory != nil {
		if l := r.Memory.GetLimit().GetValue(); l != 0 {
			g.SetLinuxResourcesMemoryLimit(l)
			g.SetLinuxResourcesMemorySwap(l)
		}
	}
	for _, l := range r.HugepageLimits {
		g.AddLinuxResourcesHugepageLimit(l.PageSize, l.Limit)
	}
	for k, v := range r.Unified {
		g.AddLinuxResourcesUnified(k, v)
	}

	if g.checkResources != nil {
		if err := g.checkResources(g.Config.Linux.Resources); err != nil {
			return fmt.Errorf("failed to adjust resources in OCI Spec: %w", err)
		}
	}

	return nil
}

// AdjustBlockIOClass adjusts the block I/O class in the OCI Spec.
func (g *Generator) AdjustBlockIOClass(blockIOClass *string) error {
	if blockIOClass == nil || g.resolveBlockIO == nil {
		return nil
	}

	if *blockIOClass == "" {
		g.ClearLinuxResourcesBlockIO()
		return nil
	}

	blockIO, err := g.resolveBlockIO(*blockIOClass)
	if err != nil {
		return fmt.Errorf("failed to adjust BlockIO class in OCI Spec: %w", err)
	}

	g.SetLinuxResourcesBlockIO(blockIO)
	return nil
}

// AdjustRdtClass adjusts the RDT class in the OCI Spec.
func (g *Generator) AdjustRdtClass(rdtClass *string) error {
	if rdtClass == nil || g.resolveRdt == nil {
		return nil
	}

	if *rdtClass == "" {
		g.ClearLinuxIntelRdt()
		return nil
	}

	rdt, err := g.resolveRdt(*rdtClass)
	if err != nil {
		return fmt.Errorf("failed to adjust RDT class in OCI Spec: %w", err)
	}

	g.SetLinuxIntelRdt(rdt)
	return nil
}

// AdjustCgroupsPath adjusts the cgroup pseudofs path in the OCI Spec.
func (g *Generator) AdjustCgroupsPath(path string) {
	if path != "" {
		g.SetLinuxCgroupsPath(path)
	}
}

// AdjustDevices adjusts the (Linux) devices in the OCI Spec.
func (g *Generator) AdjustDevices(devices []*nri.LinuxDevice) {
	for _, d := range devices {
		key, marked := d.IsMarkedForRemoval()
		g.RemoveDevice(key)
		if marked {
			continue
		}
		g.AddDevice(d.ToOCI())
		major, minor, access := &d.Major, &d.Minor, d.AccessString()
		g.AddLinuxResourcesDevice(true, d.Type, major, minor, access)
	}
}

// AdjustMounts adjusts the mounts in the OCI Spec.
func (g *Generator) AdjustMounts(mounts []*nri.Mount) error {
	var (
		propagation string
	)

	for _, m := range mounts {
		if destination, marked := m.IsMarkedForRemoval(); marked {
			g.RemoveMount(destination)
			continue
		}
		g.RemoveMount(m.Destination)

		mnt := m.ToOCI(&propagation)

		switch propagation {
		case "rprivate":
		case "rshared":
			if err := ensurePropagation(mnt.Source, "rshared"); err != nil {
				return fmt.Errorf("failed to adjust mounts in OCI Spec: %w", err)
			}
			if err := g.SetLinuxRootPropagation("rshared"); err != nil {
				return fmt.Errorf("failed to adjust rootfs propagation in OCI Spec: %w", err)
			}
		case "rslave":
			if err := ensurePropagation(mnt.Source, "rshared", "rslave"); err != nil {
				return fmt.Errorf("failed to adjust mounts in OCI Spec: %w", err)
			}
			rootProp := g.Config.Linux.RootfsPropagation
			if rootProp != "rshared" && rootProp != "rslave" {
				if err := g.SetLinuxRootPropagation("rslave"); err != nil {
					return fmt.Errorf("failed to adjust rootfs propagation in OCI Spec: %w", err)
				}
			}
		}
		g.AddMount(mnt)
	}

	return nil
}

func nopFilter(m map[string]string) (map[string]string, error) {
	return m, nil
}

//
// TODO: these could be added to the stock Spec generator...
//

// AddCreateRuntimeHook adds a hooks new CreateRuntime hooks.
func (g *Generator) AddCreateRuntimeHook(hook rspec.Hook) {
	g.initConfigHooks()
	g.Config.Hooks.CreateRuntime = append(g.Config.Hooks.CreateRuntime, hook)
}

// AddCreateContainerHook adds a hooks new CreateContainer hooks.
func (g *Generator) AddCreateContainerHook(hook rspec.Hook) {
	g.initConfigHooks()
	g.Config.Hooks.CreateContainer = append(g.Config.Hooks.CreateContainer, hook)
}

// AddStartContainerHook adds a hooks new StartContainer hooks.
func (g *Generator) AddStartContainerHook(hook rspec.Hook) {
	g.initConfigHooks()
	g.Config.Hooks.StartContainer = append(g.Config.Hooks.StartContainer, hook)
}

// ClearLinuxIntelRdt clears RDT CLOS.
func (g *Generator) ClearLinuxIntelRdt() {
	g.initConfigLinux()
	g.Config.Linux.IntelRdt = nil
}

// SetLinuxIntelRdt sets RDT CLOS.
func (g *Generator) SetLinuxIntelRdt(rdt *rspec.LinuxIntelRdt) {
	g.initConfigLinux()
	g.Config.Linux.IntelRdt = rdt
}

// ClearLinuxResourcesBlockIO clears Block I/O settings.
func (g *Generator) ClearLinuxResourcesBlockIO() {
	g.initConfigLinuxResources()
	g.Config.Linux.Resources.BlockIO = nil
}

// SetLinuxResourcesBlockIO sets Block I/O settings.
func (g *Generator) SetLinuxResourcesBlockIO(blockIO *rspec.LinuxBlockIO) {
	g.initConfigLinuxResources()
	g.Config.Linux.Resources.BlockIO = blockIO
}

func (g *Generator) initConfig() {
	if g.Config == nil {
		g.Config = &rspec.Spec{}
	}
}

func (g *Generator) initConfigHooks() {
	g.initConfig()
	if g.Config.Hooks == nil {
		g.Config.Hooks = &rspec.Hooks{}
	}
}

func (g *Generator) initConfigLinux() {
	g.initConfig()
	if g.Config.Linux == nil {
		g.Config.Linux = &rspec.Linux{}
	}
}

func (g *Generator) initConfigLinuxResources() {
	g.initConfigLinux()
	if g.Config.Linux.Resources == nil {
		g.Config.Linux.Resources = &rspec.LinuxResources{}
	}
}

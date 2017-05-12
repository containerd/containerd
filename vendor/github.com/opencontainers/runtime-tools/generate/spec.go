package generate

import (
	rspec "github.com/opencontainers/runtime-spec/specs-go"
)

func (g *Generator) initSpec() {
	if g.spec == nil {
		g.spec = &rspec.Spec{}
	}
}

func (g *Generator) initSpecAnnotations() {
	g.initSpec()
	if g.spec.Annotations == nil {
		g.spec.Annotations = make(map[string]string)
	}
}

func (g *Generator) initSpecLinux() {
	g.initSpec()
	if g.spec.Linux == nil {
		g.spec.Linux = &rspec.Linux{}
	}
}

func (g *Generator) initSpecLinuxSysctl() {
	g.initSpecLinux()
	if g.spec.Linux.Sysctl == nil {
		g.spec.Linux.Sysctl = make(map[string]string)
	}
}

func (g *Generator) initSpecLinuxSeccomp() {
	g.initSpecLinux()
	if g.spec.Linux.Seccomp == nil {
		g.spec.Linux.Seccomp = &rspec.LinuxSeccomp{}
	}
}

func (g *Generator) initSpecLinuxResources() {
	g.initSpecLinux()
	if g.spec.Linux.Resources == nil {
		g.spec.Linux.Resources = &rspec.LinuxResources{}
	}
}

func (g *Generator) initSpecLinuxResourcesCPU() {
	g.initSpecLinuxResources()
	if g.spec.Linux.Resources.CPU == nil {
		g.spec.Linux.Resources.CPU = &rspec.LinuxCPU{}
	}
}

func (g *Generator) initSpecLinuxResourcesMemory() {
	g.initSpecLinuxResources()
	if g.spec.Linux.Resources.Memory == nil {
		g.spec.Linux.Resources.Memory = &rspec.LinuxMemory{}
	}
}

func (g *Generator) initSpecLinuxResourcesNetwork() {
	g.initSpecLinuxResources()
	if g.spec.Linux.Resources.Network == nil {
		g.spec.Linux.Resources.Network = &rspec.LinuxNetwork{}
	}
}

func (g *Generator) initSpecLinuxResourcesPids() {
	g.initSpecLinuxResources()
	if g.spec.Linux.Resources.Pids == nil {
		g.spec.Linux.Resources.Pids = &rspec.LinuxPids{}
	}
}

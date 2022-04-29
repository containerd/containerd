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

package oci

import (
	"context"
	"fmt"

	"github.com/container-orchestrated-devices/container-device-interface/pkg/cdi"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cap"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// WithHostDevices adds all the hosts device nodes to the container's spec
func WithHostDevices(_ context.Context, _ Client, _ *containers.Container, s *Spec) error {
	setLinux(s)

	devs, err := HostDevices()
	if err != nil {
		return err
	}
	s.Linux.Devices = append(s.Linux.Devices, devs...)
	return nil
}

// WithDevices recursively adds devices from the passed in path and associated cgroup rules for that device.
// If devicePath is a dir it traverses the dir to add all devices in that dir.
// If devicePath is not a dir, it attempts to add the single device.
// If containerPath is not set then the device path is used for the container path.
func WithDevices(devicePath, containerPath, permissions string) SpecOpts {
	return func(_ context.Context, _ Client, _ *containers.Container, s *Spec) error {
		devs, err := getDevices(devicePath, containerPath)
		if err != nil {
			return err
		}
		for i := range devs {
			s.Linux.Devices = append(s.Linux.Devices, devs[i])
			s.Linux.Resources.Devices = append(s.Linux.Resources.Devices, specs.LinuxDeviceCgroup{
				Allow:  true,
				Type:   devs[i].Type,
				Major:  &devs[i].Major,
				Minor:  &devs[i].Minor,
				Access: permissions,
			})
		}
		return nil
	}
}

// WithMemorySwap sets the container's swap in bytes
func WithMemorySwap(swap int64) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setResources(s)
		if s.Linux.Resources.Memory == nil {
			s.Linux.Resources.Memory = &specs.LinuxMemory{}
		}
		s.Linux.Resources.Memory.Swap = &swap
		return nil
	}
}

// WithPidsLimit sets the container's pid limit or maximum
func WithPidsLimit(limit int64) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setResources(s)
		if s.Linux.Resources.Pids == nil {
			s.Linux.Resources.Pids = &specs.LinuxPids{}
		}
		s.Linux.Resources.Pids.Limit = limit
		return nil
	}
}

// WithBlockIO sets the container's blkio parameters
func WithBlockIO(blockio *specs.LinuxBlockIO) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setResources(s)
		s.Linux.Resources.BlockIO = blockio
		return nil
	}
}

// WithCPUShares sets the container's cpu shares
func WithCPUShares(shares uint64) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setCPU(s)
		s.Linux.Resources.CPU.Shares = &shares
		return nil
	}
}

// WithCPUs sets the container's cpus/cores for use by the container
func WithCPUs(cpus string) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setCPU(s)
		s.Linux.Resources.CPU.Cpus = cpus
		return nil
	}
}

// WithCPUsMems sets the container's cpu mems for use by the container
func WithCPUsMems(mems string) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setCPU(s)
		s.Linux.Resources.CPU.Mems = mems
		return nil
	}
}

// WithCPUCFS sets the container's Completely fair scheduling (CFS) quota and period
func WithCPUCFS(quota int64, period uint64) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		setCPU(s)
		s.Linux.Resources.CPU.Quota = &quota
		s.Linux.Resources.CPU.Period = &period
		return nil
	}
}

// WithAllCurrentCapabilities propagates the effective capabilities of the caller process to the container process.
// The capability set may differ from WithAllKnownCapabilities when running in a container.
var WithAllCurrentCapabilities = func(ctx context.Context, client Client, c *containers.Container, s *Spec) error {
	caps, err := cap.Current()
	if err != nil {
		return err
	}
	return WithCapabilities(caps)(ctx, client, c, s)
}

// WithAllKnownCapabilities sets all the the known linux capabilities for the container process
var WithAllKnownCapabilities = func(ctx context.Context, client Client, c *containers.Container, s *Spec) error {
	caps := cap.Known()
	return WithCapabilities(caps)(ctx, client, c, s)
}

// WithoutRunMount removes the `/run` inside the spec
func WithoutRunMount(ctx context.Context, client Client, c *containers.Container, s *Spec) error {
	return WithoutMounts("/run")(ctx, client, c, s)
}

// WithRdt sets the container's RDT parameters
func WithRdt(closID, l3CacheSchema, memBwSchema string) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		s.Linux.IntelRdt = &specs.LinuxIntelRdt{
			ClosID:        closID,
			L3CacheSchema: l3CacheSchema,
			MemBwSchema:   memBwSchema,
		}
		return nil
	}
}

func escapeAndCombineArgs(args []string) string {
	panic("not supported")
}

// WithCDI updates OCI spec with CDI content
func WithCDI(annotations map[string]string, cdiSpecDirs []string) SpecOpts {
	return func(ctx context.Context, _ Client, c *containers.Container, s *Spec) error {
		// TODO: Once CRI is extended with native CDI support this will need to be updated...
		_, cdiDevices, err := cdi.ParseAnnotations(annotations)
		if err != nil {
			return fmt.Errorf("failed to parse CDI device annotations: %w", err)
		}
		if cdiDevices == nil {
			return nil
		}

		registry := cdi.GetRegistry(cdi.WithSpecDirs(cdiSpecDirs...))
		if err = registry.Refresh(); err != nil {
			// We don't consider registry refresh failure a fatal error.
			// For instance, a dynamically generated invalid CDI Spec file for
			// any particular vendor shouldn't prevent injection of devices of
			// different vendors. CDI itself knows better and it will fail the
			// injection if necessary.
			log.G(ctx).Warnf("CDI registry refresh failed: %v", err)
		}

		if _, err := registry.InjectDevices(s, cdiDevices...); err != nil {
			return fmt.Errorf("CDI device injection failed: %w", err)
		}

		// One crucial thing to keep in mind is that CDI device injection
		// might add OCI Spec environment variables, hooks, and mounts as
		// well. Therefore it is important that none of the corresponding
		// OCI Spec fields are reset up in the call stack once we return.
		return nil
	}
}

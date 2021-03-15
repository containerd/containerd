// +build linux

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
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/pkg/cap"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// WithHostDevices adds all the hosts device nodes to the container's spec
func WithHostDevices(_ context.Context, _ Client, _ *containers.Container, s *Spec) error {
	setLinux(s)

	devs, err := getDevices("/dev")
	if err != nil {
		return err
	}
	s.Linux.Devices = append(s.Linux.Devices, devs...)
	return nil
}

var errNotADevice = errors.New("not a device node")

func getDevices(path string) ([]specs.LinuxDevice, error) {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []specs.LinuxDevice
	for _, f := range files {
		switch {
		case f.IsDir():
			switch f.Name() {
			// ".lxc" & ".lxd-mounts" added to address https://github.com/lxc/lxd/issues/2825
			// ".udev" added to address https://github.com/opencontainers/runc/issues/2093
			case "pts", "shm", "fd", "mqueue", ".lxc", ".lxd-mounts", ".udev":
				continue
			default:
				sub, err := getDevices(filepath.Join(path, f.Name()))
				if err != nil {
					return nil, err
				}

				out = append(out, sub...)
				continue
			}
		case f.Name() == "console":
			continue
		}
		device, err := deviceFromPath(filepath.Join(path, f.Name()), "rwm")
		if err != nil {
			if err == errNotADevice {
				continue
			}
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		out = append(out, *device)
	}
	return out, nil
}

func deviceFromPath(path, permissions string) (*specs.LinuxDevice, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return nil, err
	}

	var (
		// The type is 32bit on mips.
		devNumber = uint64(stat.Rdev) // nolint: unconvert
		major     = unix.Major(devNumber)
		minor     = unix.Minor(devNumber)
	)
	if major == 0 {
		return nil, errNotADevice
	}

	var (
		devType string
		mode    = stat.Mode
	)
	switch {
	case mode&unix.S_IFBLK == unix.S_IFBLK:
		devType = "b"
	case mode&unix.S_IFCHR == unix.S_IFCHR:
		devType = "c"
	}
	fm := os.FileMode(mode &^ unix.S_IFMT)
	return &specs.LinuxDevice{
		Type:     devType,
		Path:     path,
		Major:    int64(major),
		Minor:    int64(minor),
		FileMode: &fm,
		UID:      &stat.Uid,
		GID:      &stat.Gid,
	}, nil
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

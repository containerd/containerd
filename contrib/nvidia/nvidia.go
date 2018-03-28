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

package nvidia

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const nvidiaCLI = "nvidia-container-cli"

// Capability specifies capabilities for the gpu inside the container
// Detailed explaination of options can be found:
// https://github.com/nvidia/nvidia-container-runtime#supported-driver-capabilities
type Capability int

const (
	// Compute capability
	Compute Capability = iota + 1
	// Compat32 capability
	Compat32
	// Graphics capability
	Graphics
	// Utility capability
	Utility
	// Video capability
	Video
	// Display capability
	Display
)

// WithGPUs adds NVIDIA gpu support to a container
func WithGPUs(opts ...Opts) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		c := &config{}
		for _, o := range opts {
			if err := o(c); err != nil {
				return err
			}
		}
		path, err := exec.LookPath("containerd")
		if err != nil {
			return err
		}
		nvidiaPath, err := exec.LookPath(nvidiaCLI)
		if err != nil {
			return err
		}
		if s.Hooks == nil {
			s.Hooks = &specs.Hooks{}
		}
		s.Hooks.Prestart = append(s.Hooks.Prestart, specs.Hook{
			Path: path,
			Args: append([]string{
				"containerd",
				"oci-hook",
				"--",
				nvidiaPath,
			}, c.args()...),
			Env: os.Environ(),
		})
		return nil
	}
}

type config struct {
	Devices      []int
	DeviceUUID   string
	Capabilities []Capability
	LoadKmods    bool
	LDCache      string
	LDConfig     string
	Requirements []string
}

func (c *config) args() []string {
	var args []string

	if c.LoadKmods {
		args = append(args, "--load-kmods")
	}
	if c.LDCache != "" {
		args = append(args, fmt.Sprintf("--ldcache=%s", c.LDCache))
	}
	args = append(args,
		"configure",
	)
	if len(c.Devices) > 0 {
		args = append(args, fmt.Sprintf("--device=%s", strings.Join(toStrings(c.Devices), ",")))
	}
	if c.DeviceUUID != "" {
		args = append(args, fmt.Sprintf("--device=%s", c.DeviceUUID))
	}
	for _, c := range c.Capabilities {
		args = append(args, fmt.Sprintf("--%s", capFlags[c]))
	}
	if c.LDConfig != "" {
		args = append(args, fmt.Sprintf("--ldconfig=%s", c.LDConfig))
	}
	for _, r := range c.Requirements {
		args = append(args, fmt.Sprintf("--require=%s", r))
	}
	args = append(args, "--pid={{pid}}", "{{rootfs}}")
	return args
}

var capFlags = map[Capability]string{
	Compute:  "compute",
	Compat32: "compat32",
	Graphics: "graphics",
	Utility:  "utility",
	Video:    "video",
	Display:  "display",
}

func toStrings(ints []int) []string {
	var s []string
	for _, i := range ints {
		s = append(s, strconv.Itoa(i))
	}
	return s
}

// Opts are options for configuring gpu support
type Opts func(*config) error

// WithDevices adds the provided device indexes to the container
func WithDevices(ids ...int) Opts {
	return func(c *config) error {
		c.Devices = ids
		return nil
	}
}

// WithDeviceUUID adds the specific device UUID to the container
func WithDeviceUUID(guid string) Opts {
	return func(c *config) error {
		c.DeviceUUID = guid
		return nil
	}
}

// WithAllDevices adds all gpus to the container
func WithAllDevices(c *config) error {
	c.DeviceUUID = "all"
	return nil
}

// WithAllCapabilities adds all capabilities to the container for the gpus
func WithAllCapabilities(c *config) error {
	for k := range capFlags {
		c.Capabilities = append(c.Capabilities, k)
	}
	return nil
}

// WithRequiredCUDAVersion sets the required cuda version
func WithRequiredCUDAVersion(major, minor int) Opts {
	return func(c *config) error {
		c.Requirements = append(c.Requirements, fmt.Sprintf("cuda>=%d.%d", major, minor))
		return nil
	}
}

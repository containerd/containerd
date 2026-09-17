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

package server

import (
	"context"
	"os"
	"sort"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/systemd"
	"github.com/containerd/log"
	"github.com/pelletier/go-toml/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) getLinuxRuntimeConfig(ctx context.Context) *runtime.LinuxRuntimeConfiguration {
	return &runtime.LinuxRuntimeConfiguration{CgroupDriver: c.getCgroupDriver(ctx)}
}

func (c *criService) getCgroupDriver(ctx context.Context) runtime.CgroupDriver {
	// Go through the runtime handlers in a predictable order, starting from the
	// default handler, others sorted in alphabetical order
	handlerNames := make([]string, 0, len(c.config.ContainerdConfig.Runtimes))
	for n := range c.config.ContainerdConfig.Runtimes {
		handlerNames = append(handlerNames, n)
	}
	sort.Slice(handlerNames, func(i, j int) bool {
		if handlerNames[i] == c.config.ContainerdConfig.DefaultRuntimeName {
			return true
		}
		if handlerNames[j] == c.config.ContainerdConfig.DefaultRuntimeName {
			return false
		}
		return handlerNames[i] < handlerNames[j]
	})

	for _, handler := range handlerNames {
		opts, err := criconfig.GenerateRuntimeOptions(c.config.ContainerdConfig.Runtimes[handler])
		if err != nil {
			log.G(ctx).Debugf("failed to parse runtime handler options for %q", handler)
			continue
		}
		if d, ok := getCgroupDriverFromRuntimeHandlerOpts(opts); ok {
			return d
		}
		log.G(ctx).Debugf("runtime handler %q does not provide cgroup driver information", handler)
	}

	// If no runtime handlers have a setting, detect if systemd is running
	d := runtime.CgroupDriver_CGROUPFS
	if systemd.IsRunningSystemd() {
		d = runtime.CgroupDriver_SYSTEMD
	}
	log.G(ctx).Debugf("no runtime handler provided cgroup driver setting, using auto-detected %s", runtime.CgroupDriver_name[int32(d)])
	return d
}

func getCgroupDriverFromRuntimeHandlerOpts(opts any) (runtime.CgroupDriver, bool) {
	switch v := opts.(type) {
	case *runcoptions.Options:
		if v.SystemdCgroup {
			return runtime.CgroupDriver_SYSTEMD, true
		}
		return runtime.CgroupDriver_CGROUPFS, true
	case *runtimeoptions.Options:
		// Generic shims (e.g. io.containerd.runsc.v1) carry their config
		// either as an inline TOML body or via a path to a config file.
		// In both cases we look for a top-level SystemdCgroup key.
		// Only return a driver when the key is explicitly present.
		var body []byte
		switch {
		case v.ConfigPath != "":
			// Config is in a file; read it to inspect SystemdCgroup.
			data, err := os.ReadFile(v.ConfigPath)
			if err != nil {
				return runtime.CgroupDriver_CGROUPFS, false
			}
			body = data
		case len(v.ConfigBody) > 0:
			body = v.ConfigBody
		default:
			return runtime.CgroupDriver_CGROUPFS, false
		}
		var decoded map[string]any
		if err := toml.Unmarshal(body, &decoded); err != nil {
			return runtime.CgroupDriver_CGROUPFS, false
		}
		if val, ok := decoded["SystemdCgroup"]; ok {
			if b, ok := val.(bool); ok {
				if b {
					return runtime.CgroupDriver_SYSTEMD, true
				}
				return runtime.CgroupDriver_CGROUPFS, true
			}
		}
		// The runsc shim stores the cgroup driver under [runsc_config].systemd-cgroup.
		// containerd-shim-runsc-v1 expects this value as a TOML string ("true"/"false")
		// because its config struct maps the field to a Go string; accept both forms.
		if rc, ok := decoded["runsc_config"].(map[string]any); ok {
			if val, ok := rc["systemd-cgroup"]; ok {
				switch v := val.(type) {
				case bool:
					if v {
						return runtime.CgroupDriver_SYSTEMD, true
					}
					return runtime.CgroupDriver_CGROUPFS, true
				case string:
					if v == "true" {
						return runtime.CgroupDriver_SYSTEMD, true
					}
					return runtime.CgroupDriver_CGROUPFS, true
				}
			}
		}
	}
	return runtime.CgroupDriver_CGROUPFS, false
}

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

package sbserver

import (
	"context"
	"testing"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/systemd"
	"github.com/containerd/containerd/plugin"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func newFakeRuntimeConfig(t string, systemdCgroup bool) criconfig.Runtime {
	r := criconfig.Runtime{Type: t, Options: map[string]interface{}{}}
	switch t {
	case plugin.RuntimeRuncV1, plugin.RuntimeRuncV2, plugin.RuntimeLinuxV1:
		if systemdCgroup {
			r.Options["SystemdCgroup"] = true
		}
	}
	return r
}

func TestRuntimeConfig(t *testing.T) {
	autoDetected := runtime.CgroupDriver_CGROUPFS
	if systemd.IsRunningSystemd() {
		autoDetected = runtime.CgroupDriver_SYSTEMD
	}

	for _, test := range []struct {
		desc                 string
		defaultRuntime       string
		runtimes             map[string]criconfig.Runtime
		expectedCgroupDriver runtime.CgroupDriver
	}{
		{
			desc:                 "no runtimes",
			expectedCgroupDriver: autoDetected,
		},
		{
			desc:                 "non-runc runtime",
			defaultRuntime:       "non-runc",
			runtimes:             map[string]criconfig.Runtime{"non-runc": newFakeRuntimeConfig("default", false)},
			expectedCgroupDriver: autoDetected,
		},
		{
			desc: "no default, pick first in alphabetical order",
			runtimes: map[string]criconfig.Runtime{
				"non-runc":   newFakeRuntimeConfig("default", false),
				"runc-2":     newFakeRuntimeConfig(plugin.RuntimeRuncV2, true),
				"runc":       newFakeRuntimeConfig(plugin.RuntimeRuncV2, false),
				"non-runc-2": newFakeRuntimeConfig("default", false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			desc:           "pick default, cgroupfs",
			defaultRuntime: "runc-2",
			runtimes: map[string]criconfig.Runtime{
				"non-runc": newFakeRuntimeConfig("default", false),
				"runc":     newFakeRuntimeConfig(plugin.RuntimeRuncV2, true),
				"runc-2":   newFakeRuntimeConfig(plugin.RuntimeRuncV2, false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			desc:           "pick default, systemd",
			defaultRuntime: "runc-2",
			runtimes: map[string]criconfig.Runtime{
				"non-runc": newFakeRuntimeConfig("default", false),
				"runc":     newFakeRuntimeConfig(plugin.RuntimeRuncV2, false),
				"runc-2":   newFakeRuntimeConfig(plugin.RuntimeRuncV2, true),
			},
			expectedCgroupDriver: runtime.CgroupDriver_SYSTEMD,
		},
		{
			desc:           "pick default, runcv1 systemd",
			defaultRuntime: "runcv1",
			runtimes: map[string]criconfig.Runtime{
				"runc":   newFakeRuntimeConfig(plugin.RuntimeRuncV2, false),
				"runcv1": newFakeRuntimeConfig(plugin.RuntimeRuncV1, true),
			},
			expectedCgroupDriver: runtime.CgroupDriver_SYSTEMD,
		},
		{
			desc:           "pick default, linuxv1 systemd",
			defaultRuntime: "linuxv1",
			runtimes: map[string]criconfig.Runtime{
				"runc":    newFakeRuntimeConfig(plugin.RuntimeRuncV2, false),
				"linuxv1": newFakeRuntimeConfig(plugin.RuntimeLinuxV1, true),
			},
			expectedCgroupDriver: runtime.CgroupDriver_SYSTEMD,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			c.config.PluginConfig.ContainerdConfig.DefaultRuntimeName = test.defaultRuntime
			c.config.PluginConfig.ContainerdConfig.Runtimes = test.runtimes

			resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
			assert.NoError(t, err)
			assert.Equal(t, test.expectedCgroupDriver, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
		})
	}
}

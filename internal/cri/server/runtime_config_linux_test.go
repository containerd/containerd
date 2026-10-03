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
	"path/filepath"
	"testing"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/systemd"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func newFakeRuntimeConfig(runcV2, systemdCgroup bool) criconfig.Runtime {
	r := criconfig.Runtime{Type: "default", Options: map[string]any{}}
	if runcV2 {
		r.Type = plugins.RuntimeRuncV2
		if systemdCgroup {
			r.Options["SystemdCgroup"] = true
		}
	}
	return r
}

// newFakeGenericRuntimeConfig returns a runtime config for a generic shim
// (e.g. io.containerd.runsc.v1).  SystemdCgroup is carried through the TOML
// ConfigBody that GenerateRuntimeOptions populates on *runtimeoptions.Options.
func newFakeGenericRuntimeConfig(runtimeType string, systemdCgroup bool) criconfig.Runtime {
	r := criconfig.Runtime{Type: runtimeType, Options: map[string]any{
		"SystemdCgroup": systemdCgroup,
	}}
	return r
}

// newFakeRunscConfigPathRuntime returns a runtime config for the runsc shim
// that uses config_path to point at a temporary shim config file.  The file
// uses the [runsc_config] section format that containerd-shim-runsc-v1 reads.
// tomlString controls whether systemd-cgroup is written as a TOML string
// ("true"/"false") — as produced by write-runsc-shim-config.sh — or as a
// bare TOML boolean (true/false).
func newFakeRunscConfigPathRuntime(t *testing.T, runtimeType string, systemdCgroup bool, tomlString bool) criconfig.Runtime {
	t.Helper()
	var val string
	if tomlString {
		if systemdCgroup {
			val = `"true"`
		} else {
			val = `"false"`
		}
	} else {
		if systemdCgroup {
			val = "true"
		} else {
			val = "false"
		}
	}
	content := "[runsc_config]\n  systemd-cgroup = " + val + "\n"
	f := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return criconfig.Runtime{Type: runtimeType, Options: map[string]any{
		"ConfigPath": f,
	}}
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
			runtimes:             map[string]criconfig.Runtime{"non-runc": newFakeRuntimeConfig(false, false)},
			expectedCgroupDriver: autoDetected,
		},
		{
			desc: "no default, pick first in alphabetical order",
			runtimes: map[string]criconfig.Runtime{
				"non-runc":   newFakeRuntimeConfig(false, false),
				"runc-2":     newFakeRuntimeConfig(true, true),
				"runc":       newFakeRuntimeConfig(true, false),
				"non-runc-2": newFakeRuntimeConfig(false, false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			desc:           "pick default, cgroupfs",
			defaultRuntime: "runc-2",
			runtimes: map[string]criconfig.Runtime{
				"non-runc": newFakeRuntimeConfig(false, false),
				"runc":     newFakeRuntimeConfig(true, true),
				"runc-2":   newFakeRuntimeConfig(true, false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			desc:           "pick default, systemd",
			defaultRuntime: "runc-2",
			runtimes: map[string]criconfig.Runtime{
				"non-runc": newFakeRuntimeConfig(false, false),
				"runc":     newFakeRuntimeConfig(true, false),
				"runc-2":   newFakeRuntimeConfig(true, true),
			},
			expectedCgroupDriver: runtime.CgroupDriver_SYSTEMD,
		},
		{
			desc:           "generic shim (runsc), cgroupfs",
			defaultRuntime: "runsc",
			runtimes: map[string]criconfig.Runtime{
				"runsc": newFakeGenericRuntimeConfig("io.containerd.runsc.v1", false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			desc:           "generic shim (runsc), systemd",
			defaultRuntime: "runsc",
			runtimes: map[string]criconfig.Runtime{
				"runsc": newFakeGenericRuntimeConfig("io.containerd.runsc.v1", true),
			},
			expectedCgroupDriver: runtime.CgroupDriver_SYSTEMD,
		},
		{
			desc:           "generic shim (runsc) default overrides runc",
			defaultRuntime: "runsc",
			runtimes: map[string]criconfig.Runtime{
				"runc":  newFakeRuntimeConfig(true, true),
				"runsc": newFakeGenericRuntimeConfig("io.containerd.runsc.v1", false),
			},
			expectedCgroupDriver: runtime.CgroupDriver_CGROUPFS,
		},
		{
			// A generic shim with non-empty options that do NOT include
			// SystemdCgroup must fall through to host auto-detection, not be
			// silently treated as cgroupfs.
			desc:           "generic shim without SystemdCgroup key falls through to auto-detect",
			defaultRuntime: "other",
			runtimes: map[string]criconfig.Runtime{
				"other": {Type: "io.containerd.other.v1", Options: map[string]any{"SomeOtherKey": "value"}},
			},
			expectedCgroupDriver: autoDetected,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = test.defaultRuntime
			c.config.RuntimeConfig.ContainerdConfig.Runtimes = test.runtimes

			resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
			assert.NoError(t, err)
			assert.Equal(t, test.expectedCgroupDriver, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
		})
	}

	// ConfigPath cases use temp files so cannot be in the table above.
	// Test both the bare-boolean form and the TOML-string form ("true"/"false")
	// produced by write-runsc-shim-config.sh, since the shim config struct maps
	// systemd-cgroup to a Go string.
	t.Run("runsc config_path, cgroupfs (bool)", func(t *testing.T) {
		c := newTestCRIService()
		c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = "runsc"
		c.config.RuntimeConfig.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{
			"runsc": newFakeRunscConfigPathRuntime(t, "io.containerd.runsc.v1", false, false),
		}
		resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
		assert.NoError(t, err)
		assert.Equal(t, runtime.CgroupDriver_CGROUPFS, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
	})

	t.Run("runsc config_path, systemd (bool)", func(t *testing.T) {
		c := newTestCRIService()
		c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = "runsc"
		c.config.RuntimeConfig.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{
			"runsc": newFakeRunscConfigPathRuntime(t, "io.containerd.runsc.v1", true, false),
		}
		resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
		assert.NoError(t, err)
		assert.Equal(t, runtime.CgroupDriver_SYSTEMD, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
	})

	t.Run("runsc config_path, cgroupfs (string)", func(t *testing.T) {
		c := newTestCRIService()
		c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = "runsc"
		c.config.RuntimeConfig.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{
			"runsc": newFakeRunscConfigPathRuntime(t, "io.containerd.runsc.v1", false, true),
		}
		resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
		assert.NoError(t, err)
		assert.Equal(t, runtime.CgroupDriver_CGROUPFS, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
	})

	t.Run("runsc config_path, systemd (string)", func(t *testing.T) {
		c := newTestCRIService()
		c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = "runsc"
		c.config.RuntimeConfig.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{
			"runsc": newFakeRunscConfigPathRuntime(t, "io.containerd.runsc.v1", true, true),
		}
		resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
		assert.NoError(t, err)
		assert.Equal(t, runtime.CgroupDriver_SYSTEMD, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
	})

	t.Run("runsc config_path missing file falls through to auto-detect", func(t *testing.T) {
		c := newTestCRIService()
		c.config.RuntimeConfig.ContainerdConfig.DefaultRuntimeName = "runsc"
		c.config.RuntimeConfig.ContainerdConfig.Runtimes = map[string]criconfig.Runtime{
			"runsc": {Type: "io.containerd.runsc.v1", Options: map[string]any{
				"ConfigPath": "/nonexistent/runsc/config.toml",
			}},
		}
		resp, err := c.RuntimeConfig(context.TODO(), &runtime.RuntimeConfigRequest{})
		assert.NoError(t, err)
		assert.Equal(t, autoDetected, resp.Linux.CgroupDriver, "got unexpected cgroup driver")
	})
}

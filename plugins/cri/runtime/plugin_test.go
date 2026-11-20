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

package runtime

import (
	"context"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/plugins"
)

func TestCRIRuntimePluginConfigMigration(t *testing.T) {
	runcSandboxer := "podsandbox"
	cniBinDir := "/opt/cni/bin"

	grpcCri := map[string]any{
		"enable_selinux":              true,
		"max_container_log_line_size": 100,
		"max_concurrent_downloads":    3,    // removed since it's moved to cri image service
		"disable_tcp_service":         true, // removed since it's moved to cri grpc service
		"enable_cdi":                  true,
		"containerd": map[string]any{
			"runtimes": map[string]any{
				"runc": map[string]any{
					"sandbox_mode": runcSandboxer,
				},
			},
		},
		"cni": map[string]any{
			"bin_dir": cniBinDir,
		},
	}

	pluginConfigs := map[string]any{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)

	runtimeConf, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".runtime"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, runtimeConf)
	assert.Equal(t, grpcCri["enable_selinux"], runtimeConf["enable_selinux"])
	assert.Equal(t, grpcCri["max_container_log_line_size"], runtimeConf["max_container_log_line_size"])
	assert.NotContains(t, runtimeConf, "max_concurrent_downloads")
	assert.NotContains(t, runtimeConf, "disable_tcp_service")
	assert.NotContains(t, runtimeConf, "enable_cdi")

	ctd, ok := runtimeConf["containerd"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, ctd)

	runtimes := ctd["runtimes"].(map[string]any)
	runc, ok := runtimes["runc"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, runc)
	assert.Equal(t, runcSandboxer, runc["sandboxer"])
	assert.NotContains(t, runc, "sandbox_mode")

	cni, ok := runtimeConf["cni"].(map[string]any)
	require.True(t, ok)
	require.NotNil(t, cni)
	cniBinDirs, ok := cni["bin_dirs"].([]string)
	require.True(t, ok)
	require.Len(t, cniBinDirs, 1)
	require.Equal(t, cniBinDir, cniBinDirs[0])
}

// TestCRIRuntimePluginConfigMigrationBinDir ensures the removed `cni.bin_dir`
// field is renamed in place for configs already using the CRIServicePlugin
// layout (config version 3) instead of being dropped as an unknown key when
// loading config version 4.
func TestCRIRuntimePluginConfigMigrationBinDir(t *testing.T) {
	const runtimePlugin = string(plugins.CRIServicePlugin) + ".runtime"

	for _, tc := range []struct {
		name     string
		cni      map[string]any
		expected any
	}{
		{
			name: "bin_dir renamed to bin_dirs",
			cni: map[string]any{
				"bin_dir": "/opt/cni/bin",
			},
			expected: []string{"/opt/cni/bin"},
		},
		{
			name: "bin_dirs takes precedence over bin_dir",
			cni: map[string]any{
				"bin_dir":  "/opt/cni/bin",
				"bin_dirs": []string{"/custom/cni/bin"},
			},
			expected: []string{"/custom/cni/bin"},
		},
		{
			// TOML decodes arrays as []any rather than []string.
			name: "bin_dirs decoded as []any takes precedence over bin_dir",
			cni: map[string]any{
				"bin_dir":  "/opt/cni/bin",
				"bin_dirs": []any{"/custom/cni/bin", "/other/cni/bin"},
			},
			expected: []any{"/custom/cni/bin", "/other/cni/bin"},
		},
		{
			name: "empty bin_dirs is replaced by bin_dir",
			cni: map[string]any{
				"bin_dir":  "/opt/cni/bin",
				"bin_dirs": []any{},
			},
			expected: []string{"/opt/cni/bin"},
		},
		{
			name: "empty bin_dir is dropped without setting bin_dirs",
			cni: map[string]any{
				"bin_dir": "",
			},
			expected: nil,
		},
		{
			name: "empty bin_dir keeps bin_dirs",
			cni: map[string]any{
				"bin_dir":  "",
				"bin_dirs": []any{"/custom/cni/bin"},
			},
			expected: []any{"/custom/cni/bin"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pluginConfigs := map[string]any{
				runtimePlugin: map[string]any{
					"cni": tc.cni,
				},
			}
			require.NoError(t, configMigration(context.Background(), 3, pluginConfigs))

			runtimeConf, ok := pluginConfigs[runtimePlugin].(map[string]any)
			require.True(t, ok)

			cni, ok := runtimeConf["cni"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, cni, "bin_dir")
			if tc.expected == nil {
				assert.NotContains(t, cni, "bin_dirs")
			} else {
				assert.Equal(t, tc.expected, cni["bin_dirs"])
			}
		})
	}
}

// TestCRIRuntimePluginConfigMigrationBinDirTOML runs the migration on plugin
// configs decoded from real TOML, which is how the config loader produces
// them (arrays are []any).
func TestCRIRuntimePluginConfigMigrationBinDirTOML(t *testing.T) {
	const runtimePlugin = string(plugins.CRIServicePlugin) + ".runtime"

	for _, tc := range []struct {
		name     string
		toml     string
		expected []any
	}{
		{
			name: "bin_dir only",
			toml: `
[cni]
bin_dir = "/single/bin"
`,
			expected: []any{"/single/bin"},
		},
		{
			name: "bin_dir and bin_dirs",
			toml: `
[cni]
bin_dir = "/single/bin"
bin_dirs = ["/a/bin", "/b/bin"]
`,
			expected: []any{"/a/bin", "/b/bin"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runtimeConf map[string]any
			require.NoError(t, toml.Unmarshal([]byte(tc.toml), &runtimeConf))
			pluginConfigs := map[string]any{runtimePlugin: runtimeConf}

			require.NoError(t, configMigration(context.Background(), 3, pluginConfigs))

			cni, ok := runtimeConf["cni"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, cni, "bin_dir")

			// Normalize the []string produced by the rename to compare
			// against the []any produced by the TOML decoder.
			var got []any
			switch v := cni["bin_dirs"].(type) {
			case []string:
				for _, d := range v {
					got = append(got, d)
				}
			case []any:
				got = v
			}
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestCRIRuntimePluginConfigMigrationLegacyBinDir covers the legacy
// io.containerd.grpc.v1.cri layout sharing the same `bin_dir` handling.
func TestCRIRuntimePluginConfigMigrationLegacyBinDir(t *testing.T) {
	const runtimePlugin = string(plugins.CRIServicePlugin) + ".runtime"

	for _, tc := range []struct {
		name          string
		legacyDir     string
		legacyBinDirs any
		existing      any
		expected      any
	}{
		{
			name:      "bin_dir migrated",
			legacyDir: "/legacy/bin",
			expected:  []string{"/legacy/bin"},
		},
		{
			name:      "existing []any bin_dirs is kept",
			legacyDir: "/legacy/bin",
			existing:  []any{"/custom/bin"},
			expected:  []any{"/custom/bin"},
		},
		{
			name:          "legacy bin_dir does not clobber legacy bin_dirs",
			legacyDir:     "/legacy/bin",
			legacyBinDirs: []any{"/legacy/a", "/legacy/b"},
			expected:      []any{"/legacy/a", "/legacy/b"},
		},
		{
			name:      "empty bin_dir is ignored",
			legacyDir: "",
			expected:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcCni := map[string]any{"bin_dir": tc.legacyDir}
			if tc.legacyBinDirs != nil {
				srcCni["bin_dirs"] = tc.legacyBinDirs
			}
			runtimeCni := map[string]any{}
			if tc.existing != nil {
				runtimeCni["bin_dirs"] = tc.existing
			}
			pluginConfigs := map[string]any{
				string(plugins.GRPCPlugin) + ".cri": map[string]any{
					"cni": srcCni,
				},
				runtimePlugin: map[string]any{"cni": runtimeCni},
			}
			require.NoError(t, configMigration(context.Background(), 2, pluginConfigs))

			cni := pluginConfigs[runtimePlugin].(map[string]any)["cni"].(map[string]any)
			assert.NotContains(t, cni, "bin_dir")
			if tc.expected == nil {
				assert.NotContains(t, cni, "bin_dirs")
			} else {
				assert.Equal(t, tc.expected, cni["bin_dirs"])
			}
		})
	}
}

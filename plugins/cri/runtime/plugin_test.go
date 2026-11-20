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

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
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

func TestCRIRuntimePluginConfigMigrationBinDir(t *testing.T) {
	const runtimePlugin = string(plugins.CRIServicePlugin) + ".runtime"
	const legacyPlugin = string(plugins.GRPCPlugin) + ".cri"
	const legacyDir = "/legacy/bin"
	custom := []string{"/a/bin", "/b/bin"}
	defaults := criconfig.DefaultRuntimeConfig().CniConfig.NetworkPluginBinDirs
	for _, layout := range []struct {
		plugin  string
		version int
	}{
		{runtimePlugin, 3},
		{legacyPlugin, 2},
	} {
		for _, tc := range []struct {
			name    string
			fields  map[string]any
			want    []string
			wantErr bool
		}{
			{name: "legacy only", fields: map[string]any{"bin_dir": legacyDir}, want: []string{legacyDir}},
			{name: "custom list", fields: map[string]any{"bin_dir": legacyDir, "bin_dirs": custom}, want: custom},
			{name: "default list", fields: map[string]any{"bin_dir": legacyDir, "bin_dirs": defaults}, want: []string{legacyDir}},
			{name: "empty list", fields: map[string]any{"bin_dir": legacyDir, "bin_dirs": []string{}}, want: []string{legacyDir}},
			{name: "empty legacy", fields: map[string]any{"bin_dir": ""}},
			{name: "empty legacy and list", fields: map[string]any{"bin_dir": "", "bin_dirs": custom}, want: custom},
			{name: "new field only", fields: map[string]any{"bin_dirs": custom}, want: custom},
			{name: "invalid list", fields: map[string]any{"bin_dir": legacyDir, "bin_dirs": "/typo"}, wantErr: true},
			{name: "invalid list element", fields: map[string]any{"bin_dir": legacyDir, "bin_dirs": []int{1}}, wantErr: true},
		} {
			t.Run(layout.plugin+"/"+tc.name, func(t *testing.T) {
				// Loading a real TOML array into a plugin map produces []any.
				data, err := toml.Marshal(map[string]any{"cni": tc.fields})
				require.NoError(t, err)
				var pluginConf map[string]any
				require.NoError(t, toml.Unmarshal(data, &pluginConf))
				configs := map[string]any{layout.plugin: pluginConf}
				require.NoError(t, configMigration(t.Context(), layout.version, configs))
				migrated := configs[runtimePlugin].(map[string]any)
				cni := migrated["cni"].(map[string]any)
				require.NotContains(t, cni, "bin_dir")
				if tc.want == nil && !tc.wantErr {
					require.NotContains(t, cni, "bin_dirs")
				}

				data, err = toml.Marshal(migrated)
				require.NoError(t, err)
				var config criconfig.RuntimeConfig
				err = toml.Unmarshal(data, &config)
				if tc.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, config.CniConfig.NetworkPluginBinDirs)
			})
		}
	}
}

func TestCRIRuntimePluginConfigMigrationBinDirPrecedence(t *testing.T) {
	const runtimePlugin = string(plugins.CRIServicePlugin) + ".runtime"
	const legacyPlugin = string(plugins.GRPCPlugin) + ".cri"
	defaults := criconfig.DefaultRuntimeConfig().CniConfig.NetworkPluginBinDirs
	for _, tc := range []struct {
		name          string
		runtimeCNI    map[string]any
		legacyBinDirs []string
		want          any
	}{
		{name: "runtime custom list", runtimeCNI: map[string]any{"bin_dirs": []any{"/custom"}}, want: []any{"/custom"}},
		{name: "runtime default list", runtimeCNI: map[string]any{"bin_dirs": defaults}, want: defaults},
		{name: "empty runtime list", runtimeCNI: map[string]any{"bin_dirs": []string{}}, want: []string{"/legacy"}},
		{name: "empty runtime list and custom legacy list", runtimeCNI: map[string]any{"bin_dirs": []string{}}, legacyBinDirs: []string{"/custom"}, want: []string{"/custom"}},
		{name: "runtime legacy and typed defaults", runtimeCNI: map[string]any{"bin_dir": "/runtime", "bin_dirs": defaults}, want: []string{"/runtime"}},
		{name: "runtime legacy and typed custom list", runtimeCNI: map[string]any{"bin_dir": "/runtime", "bin_dirs": []string{"/custom"}}, want: []string{"/custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacyCNI := map[string]any{"bin_dir": "/legacy"}
			if tc.legacyBinDirs != nil {
				legacyCNI["bin_dirs"] = tc.legacyBinDirs
			}
			configs := map[string]any{
				legacyPlugin:  map[string]any{"cni": legacyCNI},
				runtimePlugin: map[string]any{"cni": tc.runtimeCNI},
			}
			require.NoError(t, configMigration(t.Context(), 2, configs))
			require.NotContains(t, tc.runtimeCNI, "bin_dir")
			require.Equal(t, tc.want, tc.runtimeCNI["bin_dirs"])
		})
	}
}

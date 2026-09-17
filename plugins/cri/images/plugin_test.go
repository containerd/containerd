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

package images

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxImageConfigMigration(t *testing.T) {
	image := "rancher/mirrored-pause:3.10.2-amd64"
	grpcCri := map[string]any{
		"sandbox_image": image,
	}
	pluginConfigs := map[string]any{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)
	v, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".images"]
	images := v.(map[string]any)
	require.True(t, ok)
	v, ok = images["pinned_images"]
	require.True(t, ok)
	pinnedImages := v.(map[string]any)
	v, ok = pinnedImages["sandbox"]
	require.True(t, ok)
	sandbox := v.(string)
	assert.Equal(t, image, sandbox)
}

func TestRegistryConfigMigration(t *testing.T) {
	path := "/etc/containerd/certs.d"
	grpcCri := map[string]any{
		"registry": map[string]any{
			"config_path": path,
		},
	}
	pluginConfigs := map[string]any{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)
	v, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".images"]
	images := v.(map[string]any)
	require.True(t, ok)
	v, ok = images["registry"]
	require.True(t, ok)
	registry := v.(map[string]any)
	v, ok = registry["config_path"]
	require.True(t, ok)
	configPath := v.(string)
	assert.Equal(t, path, configPath)
}

func TestRuntimePlatformsConfigMigration(t *testing.T) {
	snapshotter := "devmapper"
	grpcCri := map[string]any{
		"containerd": map[string]any{
			"runtimes": map[string]any{
				"kata": map[string]any{
					// Old per-runtime snapshotter key is "snapshotter", not "snapshot".
					"snapshotter": snapshotter,
				},
				// Runtime with no snapshotter should not appear in runtime_platforms.
				"runc": map[string]any{},
			},
		},
	}
	pluginConfigs := map[string]any{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)

	v, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".images"]
	require.True(t, ok)
	images := v.(map[string]any)

	// The destination key is "runtime_platforms" (plural), matching the TOML
	// tag on ImageConfig.RuntimePlatforms.
	v, ok = images["runtime_platforms"]
	require.True(t, ok, "runtime_platforms key must be present after migration")
	runtimePlatforms := v.(map[string]any)

	kata, ok := runtimePlatforms["kata"]
	require.True(t, ok, "kata runtime must be present in runtime_platforms")
	kataConf := kata.(map[string]any)
	assert.Equal(t, snapshotter, kataConf["snapshotter"])

	_, ok = runtimePlatforms["runc"]
	assert.False(t, ok, "runc has no snapshotter and must not appear in runtime_platforms")
}

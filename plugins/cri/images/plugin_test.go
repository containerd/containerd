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
	image := "rancher/mirrored-pause:3.10.1-amd64"
	grpcCri := map[string]interface{}{
		"sandbox_image": image,
	}
	pluginConfigs := map[string]interface{}{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)
	v, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".images"]
	images := v.(map[string]interface{})
	require.True(t, ok)
	v, ok = images["pinned_images"]
	require.True(t, ok)
	pinnedImages := v.(map[string]interface{})
	v, ok = pinnedImages["sandbox"]
	require.True(t, ok)
	sandbox := v.(string)
	assert.Equal(t, image, sandbox)
}

func TestRegistryConfigMigration(t *testing.T) {
	path := "/etc/containerd/certs.d"
	grpcCri := map[string]interface{}{
		"registry": map[string]interface{}{
			"config_path": path,
		},
	}
	pluginConfigs := map[string]interface{}{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)
	v, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".images"]
	images := v.(map[string]interface{})
	require.True(t, ok)
	v, ok = images["registry"]
	require.True(t, ok)
	registry := v.(map[string]interface{})
	v, ok = registry["config_path"]
	require.True(t, ok)
	configPath := v.(string)
	assert.Equal(t, path, configPath)
}

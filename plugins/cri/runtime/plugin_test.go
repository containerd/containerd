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

	"github.com/containerd/containerd/v2/plugins"
	"github.com/stretchr/testify/require"
)

func TestCRIRuntimePluginConfigMigration(t *testing.T) {
	runcRoot := "/run/containerd/runc"
	runcSandboxer := "podsandbox"

	grpcCri := map[string]interface{}{
		"enable_selinux":              true,
		"max_container_log_line_size": 100,
		"containerd": map[string]interface{}{
			"runtimes": map[string]interface{}{
				"runc": map[string]interface{}{
					"runtime_root": runcRoot,
					"sandbox_mode": runcSandboxer,
				},
			},
		},
	}

	pluginConfigs := map[string]interface{}{
		string(plugins.GRPCPlugin) + ".cri": grpcCri,
	}
	configMigration(context.Background(), 2, pluginConfigs)

	runtimeConf, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".runtime"].(map[string]interface{})
	require.True(t, ok)
	require.NotNil(t, runtimeConf)
	require.Equal(t, grpcCri["enable_selinux"], runtimeConf["enable_selinux"])
	require.Equal(t, grpcCri["max_container_log_line_size"], runtimeConf["max_container_log_line_size"])

	ctd, ok := runtimeConf["containerd"].(map[string]interface{})
	require.True(t, ok)
	require.NotNil(t, ctd)

	runtimes := ctd["runtimes"].(map[string]interface{})
	runc, ok := runtimes["runc"].(map[string]interface{})
	require.True(t, ok)
	require.NotNil(t, runc)
	require.Equal(t, runcSandboxer, runc["sandboxer"])
	runcOptions, ok := runc["options"].(map[string]interface{})
	require.True(t, ok)
	require.NotNil(t, runcOptions)
	require.Equal(t, runcRoot, runcOptions["Root"])
}

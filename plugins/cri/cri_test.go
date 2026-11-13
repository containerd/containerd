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

package cri

import (
	"context"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/plugins"
)

func TestCRIGRPCServerConfigMigration(t *testing.T) {
	pluginName := string(plugins.GRPCPlugin) + ".cri"

	src := map[string]any{
		// these should be removed in the new cri grpc config
		"registry": map[string]any{
			"config_path": "/etc/containerd/certs.d",
		},
		"containerd": map[string]any{
			"default_runtime_name": "runc",
		},

		// these should be kept in the new cri grpc config
		"disable_tcp_service":   false,
		"stream_server_address": "127.0.0.2",
		"stream_server_port":    "10000",
		"stream_idle_timeout":   "3h0m0s",
		"enable_tls_streaming":  true,
		"x509_key_pair_streaming": map[string]any{
			"cert_file": "/etc/containerd/certs.d/server.crt",
			"key_file":  "/etc/containerd/certs.d/server.key",
		},
		"list_streaming": map[string]any{
			"batch_size": int64(2),
		},
	}
	pluginConfigs := map[string]any{
		pluginName: src,
	}
	configMigration(context.Background(), 2, pluginConfigs)

	dst, ok := pluginConfigs[pluginName].(map[string]any)
	assert.True(t, ok)
	assert.NotNil(t, dst)

	for _, k := range []string{"registry", "containerd"} {
		_, ok := dst[k]
		assert.False(t, ok)
		delete(src, k)
	}

	for k, v := range src {
		assert.Equal(t, v, dst[k])
	}
}

func TestCRIListStreamingConfig(t *testing.T) {
	for _, test := range []struct {
		name      string
		config    string
		batchSize int
		invalid   bool
	}{
		{name: "omitted", batchSize: criconfig.DefaultStreamBatchSize},
		{name: "empty table", config: "[list_streaming]", batchSize: criconfig.DefaultStreamBatchSize},
		{name: "override", config: "[list_streaming]\nbatch_size = 2", batchSize: 2},
		{name: "zero", config: "[list_streaming]\nbatch_size = 0", invalid: true},
		{name: "negative", config: "[list_streaming]\nbatch_size = -1", batchSize: -1, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var values map[string]any
			require.NoError(t, toml.Unmarshal([]byte(test.config), &values))
			config := srvconfig.Config{Plugins: map[string]any{"io.containerd.grpc.v1.cri": values}}
			serverConfig := criconfig.DefaultServerConfig()
			_, err := config.Decode(context.Background(), "io.containerd.grpc.v1.cri", &serverConfig)
			require.NoError(t, err)
			assert.Equal(t, test.batchSize, serverConfig.ListStreaming.BatchSize)
			_, err = criconfig.ValidateServerConfig(context.Background(), &serverConfig)
			if test.invalid {
				require.ErrorContains(t, err, "`list_streaming.batch_size` must be positive")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateRuntimePlatforms(t *testing.T) {
	runtimes := map[string]criconfig.Runtime{"existing-runtime": {}}

	assert.NoError(t, validateRuntimePlatforms(runtimes, nil))
	assert.NoError(t, validateRuntimePlatforms(runtimes, map[string]criconfig.ImagePlatform{
		"existing-runtime": {},
	}))
	assert.ErrorContains(t, validateRuntimePlatforms(runtimes, map[string]criconfig.ImagePlatform{
		"runtime-not-exists": {},
	}), `runtime_platforms "runtime-not-exists" is not a configured runtime handler`)
}

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

	"github.com/containerd/containerd/v2/plugins"
	"github.com/stretchr/testify/assert"
)

func TestCRIGRPCServerConfigMigration(t *testing.T) {
	pluginName := string(plugins.GRPCPlugin) + ".cri"

	src := map[string]interface{}{
		// these should be removed in the new cri grpc config
		"registry": map[string]interface{}{
			"config_path": "/etc/containerd/certs.d",
		},
		"containerd": map[string]interface{}{
			"default_runtime_name": "runc",
		},

		// these should be kept in the new cri grpc config
		"disable_tcp_service":   false,
		"stream_server_address": "127.0.0.2",
		"stream_server_port":    "10000",
		"stream_idle_timeout":   "3h0m0s",
		"enable_tls_streaming":  true,
		"x509_key_pair_streaming": map[string]interface{}{
			"cert_file": "/etc/containerd/certs.d/server.crt",
			"key_file":  "/etc/containerd/certs.d/server.key",
		},
	}
	pluginConfigs := map[string]interface{}{
		pluginName: src,
	}
	configMigration(context.Background(), 2, pluginConfigs)

	dst, ok := pluginConfigs[pluginName].(map[string]interface{})
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

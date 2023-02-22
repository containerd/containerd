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
	"encoding/json"
	"os"
	"testing"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/services/server/config"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPrepareCreds(t *testing.T) {
	auth := v1.AuthConfig{
		Username: "useruser",
		Password: "userpassword",
	}

	data, err := prepareCreds("ghcr.io", &auth)
	assert.NoError(t, err)

	creds := ImagePullCreds{}

	err = json.Unmarshal(data, &creds)
	assert.NoError(t, err)

	assert.Equal(t, creds, ImagePullCreds{Host: "ghcr.io", User: "useruser", Secret: "userpassword"})
}

func TestLoadConfig(t *testing.T) {
	c := `
version = 2
root = "/var/lib/containerd"
state = "/run/containerd"

[grpc]
  max_recv_message_size = 16777216
  max_send_message_size = 16777216

[debug]
  address = "/run/containerd/debug.sock"

[plugins."io.containerd.grpc.v1.cri"]
  stream_server_address = "127.0.0.1"
  max_container_log_line_size = -1
  disable_cgroup = false
  [plugins."io.containerd.grpc.v1.cri".containerd]
    default_runtime_name = "runc"
    [plugins."io.containerd.grpc.v1.cri".containerd.image_distribution_helper]
      bin_path = "/opt/containerd-idp-nydus"
	`

	tmpFile, err := os.CreateTemp("", "config_test.toml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	tmpFile.Write([]byte(c))

	var containerdConfig config.Config

	config.LoadConfig(tmpFile.Name(), &containerdConfig)

	data := containerdConfig.Plugins["io.containerd.grpc.v1.cri"]

	var criConfig criconfig.Config
	err = data.Unmarshal(&criConfig)
	assert.NoError(t, err)
	assert.Equal(t, GetImageDistributionHelperBinary(&criConfig), "/opt/containerd-idp-nydus")
}

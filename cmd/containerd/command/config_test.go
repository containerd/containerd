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
package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
)

func TestCommandConfig(t *testing.T) {
	// this will fail to map to structs though in containerd 2.x, but for testing we can still use it
	data1 := `
version = 2
[plugins."io.containerd.grpc.v1.cri".registry.configs."registry-1.docker.io".auth]
  username = "${username}"
  password = "${password}"
`
	// sandbox_image also won't map
	data2 := `
[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "${sandbox_image}"
`
	// from now on all should be okay
	data3 := `
[plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/cm/local/apps/containerd/var/etc/certs.d"
`
	data4 := `
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
    SystemdCgroup = true
`
	data5 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/cm/local/apps/kubernetes/current/bin/cni"
`
	data6 := `
[plugins]
  [plugins."io.containerd.grpc.v1.cri"]
    [plugins."io.containerd.grpc.v1.cri".containerd]
      default_runtime_name = "nvidia"
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]
        [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia]
          privileged_without_host_devices = false
          runtime_type = "io.containerd.runc.v2"
          [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia.options]
            BinaryName = "/usr/bin/nvidia-container-runtime"
            SystemdCgroup = true
`
	expected := `
[plugins]
  [plugins.'io.containerd.grpc.v1.cri']
    sandbox_image = '${sandbox_image}'

    [plugins.'io.containerd.grpc.v1.cri'.cni]
      bin_dir = '/cm/local/apps/kubernetes/current/bin/cni'

    [plugins.'io.containerd.grpc.v1.cri'.containerd]
      default_runtime_name = 'nvidia'

      [plugins.'io.containerd.grpc.v1.cri'.containerd.runtimes]
        [plugins.'io.containerd.grpc.v1.cri'.containerd.runtimes.nvidia]
          privileged_without_host_devices = false
          runtime_type = 'io.containerd.runc.v2'

          [plugins.'io.containerd.grpc.v1.cri'.containerd.runtimes.nvidia.options]
            BinaryName = '/usr/bin/nvidia-container-runtime'
            SystemdCgroup = true

        [plugins.'io.containerd.grpc.v1.cri'.containerd.runtimes.runc]
          runtime_type = 'io.containerd.runc.v2'

          [plugins.'io.containerd.grpc.v1.cri'.containerd.runtimes.runc.options]
            SystemdCgroup = true

    [plugins.'io.containerd.grpc.v1.cri'.registry]
      config_path = '/cm/local/apps/containerd/var/etc/certs.d'

      [plugins.'io.containerd.grpc.v1.cri'.registry.configs]
        [plugins.'io.containerd.grpc.v1.cri'.registry.configs.'registry-1.docker.io']
          [plugins.'io.containerd.grpc.v1.cri'.registry.configs.'registry-1.docker.io'.auth]
            password = '${password}'
            username = '${username}'
`
	// currently we cannot invoke more than one times, due to:
	//  panic: io.containerd.content.v1.content: plugin: id already registered
	testMergeConfig(t, []string{data1, data2, data3, data4, data5, data6}, expected)
}

func testMergeConfig(t *testing.T, inputs []string, expected string) {
	tempDir := t.TempDir()
	var result srvconfig.Config

	for i, data := range inputs {
		// write input to a file on disk
		inputFilePath := filepath.Join(tempDir, fmt.Sprintf("data%d.toml", i+1))
		err := os.WriteFile(inputFilePath, []byte(data), 0600)
		assert.NoError(t, err)

		// append it to the main config as an import statement
		result.Imports = append(result.Imports, inputFilePath)
	}

	// now write the main config file that imports all written files so far
	mainFilepath := filepath.Join(tempDir, "containerd.toml")
	resultString, err := toml.Marshal(result)
	assert.NoError(t, err)

	err = os.WriteFile(mainFilepath, resultString, 0600)
	assert.NoError(t, err)

	// now load this config, and see if all the imports it will do results in a config containing the expected string
	config := defaultConfig()
	ctx := context.Background()
	err = srvconfig.LoadConfig(ctx, mainFilepath, config)
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = generateConfig(ctx, config, &buf)
	assert.NoError(t, err)
	assert.Contains(t, buf.String(), expected)
}

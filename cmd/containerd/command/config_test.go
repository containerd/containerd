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

	"github.com/pelletier/go-toml/v2"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	// without the following two includes the behavior of this unit test would be different
	_ "github.com/containerd/containerd/v2/plugins/cri"
	_ "github.com/containerd/containerd/v2/plugins/cri/runtime"
	"github.com/stretchr/testify/assert"
)

func TestCommandConfig(t *testing.T) {
	// deprecated but still accepted at the time of writing
	data1 := ` version = 2
	[plugins."io.containerd.grpc.v1.runtime".registry.configs."registry-1.docker.io".auth]
	 username = "my-username"
	 password = "my-password"
	`
	// the old location, should not be accepted at all
	data2 := ` version = 2
	[plugins."io.containerd.grpc.v1.cri".registry.configs."registry-1.docker.io".auth]
	 username = "should-not-be-present"
	 password = "should-not-be-present"
	`
	data3 := `
	[plugins."io.containerd.grpc.v1.runtime"]
	 sandbox_image = "my-sandbox-image:1.0"
	`
	data4 := `
	[plugins."io.containerd.grpc.v1.runtime".registry]
	 config_path = "/my-custom-certs.d-config-path"
	`
	data5 := `
	[plugins."io.containerd.grpc.v1.runtime".containerd.runtimes.runc]
	 runtime_type = "io.containerd.runc.v2"
	 [plugins."io.containerd.grpc.v1.runtime".containerd.runtimes.runc.options]
	  SystemdCgroup = true
	`
	data6 := `
	[plugins.'io.containerd.grpc.v1.cri'.cni]
	 bin_dir = '/should-not-be-present'
	 conf_dir = '/should-not-be-present'
`
	data7 := `
	[plugins.'io.containerd.grpc.v1.runtime'.cni]
	 bin_dir = '/custom-bin-dir'
`
	data8 := `
	[plugins.'io.containerd.grpc.v1.runtime'.cni]
	 conf_dir = '/custom-conf-dir'
`
	data9 := `
	[plugins]
	 [plugins."io.containerd.grpc.v1.runtime"]
	   [plugins."io.containerd.grpc.v1.runtime".containerd]
	     default_runtime_name = "nvidia"
	     [plugins."io.containerd.grpc.v1.runtime".containerd.runtimes]
	       [plugins."io.containerd.grpc.v1.runtime".containerd.runtimes.nvidia]
	         privileged_without_host_devices = false
	         runtime_type = "io.containerd.runc.v2"
	         [plugins."io.containerd.grpc.v1.runtime".containerd.runtimes.nvidia.options]
	           BinaryName = "/usr/bin/nvidia-container-runtime"
	           SystemdCgroup = true
	`
	expectedRuntimes := `
    [plugins.'io.containerd.grpc.v1.runtime'.containerd]
      default_runtime_name = 'nvidia'

      [plugins.'io.containerd.grpc.v1.runtime'.containerd.runtimes]
        [plugins.'io.containerd.grpc.v1.runtime'.containerd.runtimes.nvidia]
          privileged_without_host_devices = false
          runtime_type = 'io.containerd.runc.v2'

          [plugins.'io.containerd.grpc.v1.runtime'.containerd.runtimes.nvidia.options]
            BinaryName = '/usr/bin/nvidia-container-runtime'
            SystemdCgroup = true

        [plugins.'io.containerd.grpc.v1.runtime'.containerd.runtimes.runc]
          runtime_type = 'io.containerd.runc.v2'

          [plugins.'io.containerd.grpc.v1.runtime'.containerd.runtimes.runc.options]
            SystemdCgroup = true
`
	// currently we cannot invoke testMergeConfig() more than once, due to:
	//  panic: io.containerd.content.v1.content: plugin: id already registered
	asserts := []CheckAsserts{
		{Expected: false, Value: "should-not-be-present"},
		{Expected: true, Value: "/custom-bin-dir"},
		{Expected: true, Value: "/custom-conf-dir"},
		{Expected: true, Value: "my-username"},
		{Expected: true, Value: "my-password"},
		{Expected: true, Value: "my-sandbox-image:1.0"},
		{Expected: true, Value: "my-sandbox-image:1.0"},
		{Expected: true, Value: "/my-custom-certs.d-config-path"},
		{Expected: true, Value: expectedRuntimes},
	}
	testMergeConfig(t, []string{data1, data2, data3, data4, data5, data6, data7, data8, data9}, asserts)
}

type CheckAsserts struct {
	Expected bool
	Value    string
}

func testMergeConfig(t *testing.T, inputs []string, asserts []CheckAsserts) {
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

	// now load this config, and see if all the imports results in what we expect
	config := defaultConfig()
	ctx := context.Background()
	err = srvconfig.LoadConfig(ctx, mainFilepath, config)
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = generateConfig(ctx, config, &buf)
	assert.NoError(t, err)

	for _, item := range asserts {
		if item.Expected {
			assert.Contains(t, buf.String(), item.Value)
		} else {
			assert.NotContains(t, buf.String(), item.Value)
		}
	}
}

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

package config

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/containerd/log/logtest"
)

func TestMigrations(t *testing.T) {
	if len(migrations) != CurrentConfigVersion {
		t.Fatalf("Migration missing, expected %d migrations, only %d defined", CurrentConfigVersion, len(migrations))
	}
}

func TestMergeConfigs(t *testing.T) {
	a := &Config{
		Version:          2,
		Root:             "old_root",
		RequiredPlugins:  []string{"io.containerd.old_plugin.v1"},
		DisabledPlugins:  []string{"io.containerd.old_plugin.v1"},
		State:            "old_state",
		OOMScore:         1,
		Timeouts:         map[string]string{"a": "1"},
		StreamProcessors: map[string]StreamProcessor{"1": {Path: "2", Returns: "4"}, "2": {Path: "5"}},
	}

	b := &Config{
		Version:          2,
		Root:             "new_root",
		RequiredPlugins:  []string{"io.containerd.new_plugin1.v1", "io.containerd.new_plugin2.v1"},
		DisabledPlugins:  []string{"io.containerd.old_plugin.v1"},
		OOMScore:         2,
		Timeouts:         map[string]string{"b": "2"},
		StreamProcessors: map[string]StreamProcessor{"1": {Path: "3"}},
	}

	err := mergeConfig(a, b)
	assert.NoError(t, err)

	assert.Equal(t, 2, a.Version)
	assert.Equal(t, "new_root", a.Root)
	assert.Equal(t, "old_state", a.State)
	assert.Equal(t, 2, a.OOMScore)
	assert.Equal(t, []string{"io.containerd.old_plugin.v1", "io.containerd.new_plugin1.v1", "io.containerd.new_plugin2.v1"}, a.RequiredPlugins)
	assert.Equal(t, []string{"io.containerd.old_plugin.v1"}, a.DisabledPlugins)
	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, a.Timeouts)
	assert.Equal(t, map[string]StreamProcessor{"1": {Path: "3"}, "2": {Path: "5"}}, a.StreamProcessors)

	// Verify overrides for integers
	// https://github.com/containerd/containerd/blob/v1.6.0/services/server/config/config.go#L322-L323
	a = &Config{Version: 2, OOMScore: 1}
	b = &Config{Version: 2, OOMScore: 0} // OOMScore "not set / default"
	err = mergeConfig(a, b)
	assert.NoError(t, err)
	assert.Equal(t, 1, a.OOMScore)

	a = &Config{Version: 2, OOMScore: 1}
	b = &Config{Version: 2, OOMScore: 0} // OOMScore "not set / default"
	err = mergeConfig(a, b)
	assert.NoError(t, err)
	assert.Equal(t, 1, a.OOMScore)
}

func TestResolveImports(t *testing.T) {
	tempDir := t.TempDir()

	for _, filename := range []string{"config_1.toml", "config_2.toml", "test.toml"} {
		err := os.WriteFile(filepath.Join(tempDir, filename), []byte(""), 0o600)
		assert.NoError(t, err)
	}

	imports, err := resolveImports(filepath.Join(tempDir, "root.toml"), []string{
		filepath.Join(tempDir, "config_*.toml"), // Glob
		filepath.Join(tempDir, "./test.toml"),   // Path clean up
		"current.toml",                          // Resolve current working dir
	})
	assert.NoError(t, err)

	assert.Equal(t, imports, []string{
		filepath.Join(tempDir, "config_1.toml"),
		filepath.Join(tempDir, "config_2.toml"),
		filepath.Join(tempDir, "test.toml"),
		filepath.Join(tempDir, "current.toml"),
	})
}

func TestLoadSingleConfig(t *testing.T) {
	data := `
version = 2
root = "/var/lib/containerd"

[stream_processors]
  [stream_processors."io.containerd.processor.v1.pigz"]
	accepts = ["application/vnd.docker.image.rootfs.diff.tar.gzip"]
	path = "unpigz"
`
	tempDir := t.TempDir()

	path := filepath.Join(tempDir, "config.toml")
	err := os.WriteFile(path, []byte(data), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), path, &out)
	assert.NoError(t, err)
	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.Equal(t, map[string]StreamProcessor{
		"io.containerd.processor.v1.pigz": {
			Accepts: []string{"application/vnd.docker.image.rootfs.diff.tar.gzip"},
			Path:    "unpigz",
		},
	}, out.StreamProcessors)
}

func TestLoadConfigWithImports(t *testing.T) {
	data1 := `
version = 2
root = "/var/lib/containerd"
imports = ["data2.toml"]
`

	data2 := `
disabled_plugins = ["io.containerd.v1.xyz"]
`

	tempDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0o600)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.Equal(t, []string{"io.containerd.v1.xyz"}, out.DisabledPlugins)
}

func TestLoadConfigWithCircularImports(t *testing.T) {
	data1 := `
version = 2
root = "/var/lib/containerd"
imports = ["data2.toml", "data1.toml"]
`

	data2 := `
disabled_plugins = ["io.containerd.v1.xyz"]
imports = ["data1.toml", "data2.toml"]
`
	tempDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0o600)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.Equal(t, []string{"io.containerd.v1.xyz"}, out.DisabledPlugins)

	sort.Strings(out.Imports)
	assert.Equal(t, []string{
		"data1.toml",
		"data2.toml",
	}, out.Imports)
}

func TestDecodePlugin(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	data := `
version = 2
[plugins."io.containerd.runtime.v2.task"]
  shim_debug = true
`

	tempDir := t.TempDir()

	path := filepath.Join(tempDir, "config.toml")
	err := os.WriteFile(path, []byte(data), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), path, &out)
	assert.NoError(t, err)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(ctx, "io.containerd.runtime.v2.task", &pluginConfig)
	assert.NoError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

// TestDecodePluginInV1Config tests decoding non-versioned config
// (should be parsed as V1 config) and migrated to latest.
func TestDecodePluginInV1Config(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	data := `
[plugins.task]
  shim_debug = true
`

	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(data), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), path, &out)
	assert.NoError(t, err)
	assert.Equal(t, 0, out.Version)

	err = out.MigrateConfig(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 3, out.Version)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(ctx, "io.containerd.runtime.v2.task", &pluginConfig)
	assert.NoError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

func TestMergingTwoPluginConfigs(t *testing.T) {
	// Configuration that customizes the cni bin_dir
	data1 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/cm/local/apps/kubernetes/current/bin/cni"
`
	// Configuration that customizes the registry config_path
	data2 := `
[plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/cm/local/apps/containerd/var/etc/certs.d"
`
	// Write both to disk
	tempDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NoError(t, err)

	// Parse them both
	var out Config
	var out2 Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data2.toml"), &out2)
	assert.NoError(t, err)

	// Merge into one config
	err = mergeConfig(&out, &out2)
	assert.NoError(t, err)

	// Test if all values are present
	criPlugin := out.Plugins["io.containerd.grpc.v1.cri"]
	assert.Equal(t, criPlugin, map[string]interface{}{
		// originating from first config
		"cni": map[string]interface{}{
			"bin_dir": "/cm/local/apps/kubernetes/current/bin/cni",
		},
		// originating from second config
		"registry": map[string]interface{}{
			"config_path": "/cm/local/apps/containerd/var/etc/certs.d",
		},
	})
}

func TestMergingTwoPluginConfigsMerge(t *testing.T) {
	// Configuration that customizes the cni bin_dir
	data1 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/cm/local/apps/kubernetes/current/bin/cni"
`
	// Configuration that customizes the cni conf_dir
	data2 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    conf_dir = "/tmp"
`
	// Write both to disk
	tempDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NoError(t, err)

	// Parse them both
	var out Config
	var out2 Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data2.toml"), &out2)
	assert.NoError(t, err)

	// Merge into one config
	err = mergeConfig(&out, &out2)
	assert.NoError(t, err)

	// Test if all values are present
	criPlugin := out.Plugins["io.containerd.grpc.v1.cri"]
	assert.Equal(t, criPlugin, map[string]interface{}{
		// originating from first config
		"cni": map[string]interface{}{
			"conf_dir": "/tmp",
			// bin_dir is also preserved
			"bin_dir": "/cm/local/apps/kubernetes/current/bin/cni",
		},
	})

	// Restore first config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	// Test the other way around
	err = mergeConfig(&out2, &out)
	assert.NoError(t, err)

	// Test if all values are present
	criPlugin = out.Plugins["io.containerd.grpc.v1.cri"]
	assert.Equal(t, criPlugin, map[string]interface{}{
		// originating from first config
		"cni": map[string]interface{}{
			"bin_dir": "/cm/local/apps/kubernetes/current/bin/cni",
			// conf_dir is also preserved
			"conf_dir": "/tmp",
		},
	})
}

func TestMergingTwoPluginConfigsRecursively(t *testing.T) {
	// Configuration that configures runtime: runc
	data1 := `
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
    SystemdCgroup = true
`
	// Configuration that configures runtime: nvidia (and makes it default)
	data2 := `
[plugins]
  [plugins."io.containerd.grpc.v1.cri"]
    [plugins."io.containerd.grpc.v1.cri".containerd]
      default_runtime_name = "nvidia"
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]
        [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia]
          privileged_without_host_devices = false
          runtime_engine = ""
          runtime_root = ""
          runtime_type = "io.containerd.runc.v2"
          [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nvidia.options]
            BinaryName = "/usr/bin/nvidia-container-runtime"
            SystemdCgroup = true
`
	// Write both to disk
	tempDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NoError(t, err)

	// Parse them both
	var out Config
	var out2 Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data2.toml"), &out2)
	assert.NoError(t, err)

	// Merge into one config
	err = mergeConfig(&out, &out2)
	assert.NoError(t, err)

	// Test if both runtimes are present
	criPlugin := out.Plugins["io.containerd.grpc.v1.cri"]
	expectedConfig := map[string]interface{}{
		"containerd": map[string]interface{}{
			"default_runtime_name": string("nvidia"),
			"runtimes": map[string]interface{}{
				"nvidia": map[string]interface{}{
					"options": map[string]interface{}{
						"BinaryName":    "/usr/bin/nvidia-container-runtime",
						"SystemdCgroup": bool(true),
					},
					"privileged_without_host_devices": bool(false),
					"runtime_engine":                  string(""),
					"runtime_root":                    string(""),
					"runtime_type":                    string("io.containerd.runc.v2"),
				},
				"runc": map[string]interface{}{
					"options":      map[string]interface{}{"SystemdCgroup": bool(true)},
					"runtime_type": string("io.containerd.runc.v2"),
				},
			},
		},
	}
	assert.Equal(t, criPlugin, expectedConfig)

	// Restore first config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	// Test the other way around
	err = mergeConfig(&out2, &out)
	assert.NoError(t, err)

	// Test if both runtimes are present
	criPlugin = out.Plugins["io.containerd.grpc.v1.cri"]
	assert.Equal(t, criPlugin, expectedConfig)
}

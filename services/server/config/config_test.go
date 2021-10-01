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
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/containerd/containerd/plugin"
)

func TestMergeConfigs(t *testing.T) {
	a := &Config{
		Version:          2,
		Root:             "old_root",
		RequiredPlugins:  []string{"old_plugin"},
		DisabledPlugins:  []string{"old_plugin"},
		State:            "old_state",
		OOMScore:         1,
		Timeouts:         map[string]string{"a": "1"},
		StreamProcessors: map[string]StreamProcessor{"1": {Path: "2", Returns: "4"}, "2": {Path: "5"}},
	}

	b := &Config{
		Root:             "new_root",
		RequiredPlugins:  []string{"new_plugin1", "new_plugin2"},
		OOMScore:         2,
		Timeouts:         map[string]string{"b": "2"},
		StreamProcessors: map[string]StreamProcessor{"1": {Path: "3"}},
	}

	err := mergeConfig(a, b)
	assert.NilError(t, err)

	assert.Equal(t, a.Version, 2)
	assert.Equal(t, a.Root, "new_root")
	assert.Equal(t, a.State, "old_state")
	assert.Equal(t, a.OOMScore, 2)
	assert.DeepEqual(t, a.RequiredPlugins, []string{"old_plugin", "new_plugin1", "new_plugin2"})
	assert.DeepEqual(t, a.DisabledPlugins, []string{"old_plugin"})
	assert.DeepEqual(t, a.Timeouts, map[string]string{"a": "1", "b": "2"})
	assert.DeepEqual(t, a.StreamProcessors, map[string]StreamProcessor{"1": {Path: "3"}, "2": {Path: "5"}})
}

func TestResolveImports(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	for _, filename := range []string{"config_1.toml", "config_2.toml", "test.toml"} {
		err = os.WriteFile(filepath.Join(tempDir, filename), []byte(""), 0600)
		assert.NilError(t, err)
	}

	imports, err := resolveImports(filepath.Join(tempDir, "root.toml"), []string{
		filepath.Join(tempDir, "config_*.toml"), // Glob
		filepath.Join(tempDir, "./test.toml"),   // Path clean up
		"current.toml",                          // Resolve current working dir
	})
	assert.NilError(t, err)

	assert.DeepEqual(t, imports, []string{
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
	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "config.toml")
	err = os.WriteFile(path, []byte(data), 0600)
	assert.NilError(t, err)

	var out Config
	err = LoadConfig(path, &out)
	assert.NilError(t, err)
	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.DeepEqual(t, map[string]StreamProcessor{
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

	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NilError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NilError(t, err)

	var out Config
	err = LoadConfig(filepath.Join(tempDir, "data1.toml"), &out)
	assert.NilError(t, err)

	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.DeepEqual(t, []string{"io.containerd.v1.xyz"}, out.DisabledPlugins)
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
	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NilError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NilError(t, err)

	var out Config
	err = LoadConfig(filepath.Join(tempDir, "data1.toml"), &out)
	assert.NilError(t, err)

	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.DeepEqual(t, []string{"io.containerd.v1.xyz"}, out.DisabledPlugins)

	sort.Strings(out.Imports)
	assert.DeepEqual(t, []string{
		filepath.Join(tempDir, "data1.toml"),
		filepath.Join(tempDir, "data2.toml"),
	}, out.Imports)
}

func TestDecodePlugin(t *testing.T) {
	data := `
version = 2
[plugins."io.containerd.runtime.v1.linux"]
  shim_debug = true
`

	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "config.toml")
	err = os.WriteFile(path, []byte(data), 0600)
	assert.NilError(t, err)

	var out Config
	err = LoadConfig(path, &out)
	assert.NilError(t, err)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(&plugin.Registration{Type: "io.containerd.runtime.v1", ID: "linux", Config: &pluginConfig})
	assert.NilError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

// TestDecodePluginInV1Config tests decoding non-versioned
// config (should be parsed as V1 config).
func TestDecodePluginInV1Config(t *testing.T) {
	data := `
[plugins.linux]
  shim_debug = true
`

	tempDir, err := os.MkdirTemp("", "containerd_")
	assert.NilError(t, err)
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "config.toml")
	err = os.WriteFile(path, []byte(data), 0600)
	assert.NilError(t, err)

	var out Config
	err = LoadConfig(path, &out)
	assert.NilError(t, err)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(&plugin.Registration{ID: "linux", Config: &pluginConfig})
	assert.NilError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

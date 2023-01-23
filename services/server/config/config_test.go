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

	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/plugin"
)

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

func TestMergePlugins(t *testing.T) {
	t.Run("MergeNilTo", func(t *testing.T) {
		to := &Config{
			Plugins: nil,
		}
		from := &Config{
			Plugins: map[string]toml.Tree{},
		}

		mergePlugins(to, from)
		assert.Equal(t, from.Plugins, to.Plugins)
	})

	t.Run("MergeEmptykey", func(t *testing.T) {
		to := &Config{
			Plugins: map[string]toml.Tree{},
		}
		from := &Config{
			Plugins: map[string]toml.Tree{
				"foo": {},
			},
		}
		mergePlugins(to, from)
		assert.Equal(t, from.Plugins["foo"], to.Plugins["foo"])
	})

	t.Run("MergeTrees", func(t *testing.T) {
		to := &Config{
			Plugins: map[string]toml.Tree{},
		}
		// toml.Tree.Set() panics if the map is nil, so we need to
		// initialize it. We only need this  on tests because unmarshalling
		// from a file will always generate a map even if it's empty.
		toBar := toml.Tree{}
		toBar.SetValues(map[string]interface{}{})
		to.Plugins["bar"] = toBar

		from := &Config{
			Plugins: map[string]toml.Tree{},
		}
		fromBar := toml.Tree{}
		fromBar.SetValues(map[string]interface{}{
			"foo": "bar",
		})
		from.Plugins["bar"] = fromBar

		mergePlugins(to, from)

		resultBar := to.Plugins["bar"]
		assert.Equal(t, fromBar.Get("foo"), resultBar.Get("foo"))
	})
}

func TestMergeTrees(t *testing.T) {
	var to, from toml.Tree
	to.SetValues(map[string]interface{}{
		"foo": "bar",
	})

	from.SetValues(map[string]interface{}{
		"foo": "wrong value",
		"bar": "bar",
	})

	toSubTree := &toml.Tree{}
	toSubTree.SetValues(map[string]interface{}{
		"foo": "bar",
	})

	fromSubTree := &toml.Tree{}
	fromSubTree.SetValues(map[string]interface{}{
		"bar": "bar",
		"foo": "wrong value",
	})
	to.Set("subtree", toSubTree)
	from.Set("subtree", fromSubTree)

	mergeTrees(to, from)

	resultSubTree := to.Get("subtree").(*toml.Tree)
	assert.Equal(t, from.Get("bar"), to.Get("bar"))
	assert.NotEqual(t, from.Get("foo"), to.Get("foo"))
	assert.Equal(t, toSubTree.Get("foo"), resultSubTree.Get("foo"))
	assert.Equal(t, fromSubTree.Get("bar"), resultSubTree.Get("bar"))
	assert.NotEqual(t, fromSubTree.Get("foo"), resultSubTree.Get("foo"))

}

func TestResolveImports(t *testing.T) {
	tempDir := t.TempDir()

	for _, filename := range []string{"config_1.toml", "config_2.toml", "test.toml"} {
		err := os.WriteFile(filepath.Join(tempDir, filename), []byte(""), 0600)
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
	err := os.WriteFile(path, []byte(data), 0600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(path, &out)
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

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(filepath.Join(tempDir, "data1.toml"), &out)
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

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0600)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	assert.Equal(t, 2, out.Version)
	assert.Equal(t, "/var/lib/containerd", out.Root)
	assert.Equal(t, []string{"io.containerd.v1.xyz"}, out.DisabledPlugins)

	sort.Strings(out.Imports)
	assert.Equal(t, []string{
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

	tempDir := t.TempDir()

	path := filepath.Join(tempDir, "config.toml")
	err := os.WriteFile(path, []byte(data), 0600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(path, &out)
	assert.NoError(t, err)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(&plugin.Registration{Type: "io.containerd.runtime.v1", ID: "linux", Config: &pluginConfig})
	assert.NoError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

// TestDecodePluginInV1Config tests decoding non-versioned
// config (should be parsed as V1 config).
func TestDecodePluginInV1Config(t *testing.T) {
	data := `
[plugins.linux]
  shim_debug = true
`

	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(data), 0600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(path, &out)
	assert.NoError(t, err)

	pluginConfig := map[string]interface{}{}
	_, err = out.Decode(&plugin.Registration{ID: "linux", Config: &pluginConfig})
	assert.NoError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

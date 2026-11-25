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
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Register the builtin plugins so that their config migrations run.
	_ "github.com/containerd/containerd/v2/cmd/containerd/builtins"
	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/version"
)

func TestMigrateConfig(t *testing.T) {
	latest := int64(version.ConfigVersion)

	for _, tc := range []struct {
		name   string
		config string
		check  func(t *testing.T, out map[string]any)
	}{
		{
			name: "TopLevelValuesAreKept",
			config: `version = 4
root = "/data"
imports = ["/nonexistent/*.toml"]

[debug]
  level = "debug"

[timeouts]
  "io.containerd.timeout.shim.load" = "9s"

[stream_processors.foo]
  path = "decoder"
`,
			check: func(t *testing.T, out map[string]any) {
				assert.Equal(t, map[string]any{
					"version":  latest,
					"root":     "/data",
					"imports":  []any{"/nonexistent/*.toml"},
					"debug":    map[string]any{"level": "debug"},
					"timeouts": map[string]any{"io.containerd.timeout.shim.load": "9s"},
					"stream_processors": map[string]any{
						"foo": map[string]any{
							"accepts": []any{},
							"returns": "",
							"path":    "decoder",
							"args":    []any{},
							"env":     []any{},
						},
					},
				}, out)
			},
		},
		{
			// Entries of stream_processors and proxy_plugins replace
			// existing entries as a whole when loaded, an empty entry or
			// value is not the same as unset.
			name: "EmptyMapEntriesAreKept",
			config: `version = 4

[stream_processors."io.containerd.ocicrypt.decoder.v1.tar"]

[proxy_plugins.foo]
  type = "snapshot"
  address = "/run/foo.sock"
  [proxy_plugins.foo.exports]
    root = ""
`,
			check: func(t *testing.T, out map[string]any) {
				assert.ElementsMatch(t, []string{"version", "stream_processors", "proxy_plugins"}, slices.Collect(maps.Keys(out)))
				assert.Contains(t, lookup(t, out, "stream_processors"), "io.containerd.ocicrypt.decoder.v1.tar")
				assert.Equal(t, map[string]any{"root": ""}, lookup(t, out, "proxy_plugins", "foo", "exports"))
			},
		},
		{
			name: "LatestVersionPluginConfigIsUnchanged",
			config: `version = 4

[plugins."io.containerd.cri.v1.runtime".cni]
  conf_dir = "/etc/cni/net.d"
`,
			check: func(t *testing.T, out map[string]any) {
				assert.Equal(t, map[string]any{
					"version": latest,
					"plugins": map[string]any{
						"io.containerd.cri.v1.runtime": map[string]any{
							"cni": map[string]any{"conf_dir": "/etc/cni/net.d"},
						},
					},
				}, out)
			},
		},
		{
			name: "LegacyGRPCMovedToPlugin",
			config: `version = 2

[grpc]
  address = "/run/x.sock"
`,
			check: func(t *testing.T, out map[string]any) {
				assert.ElementsMatch(t, []string{"version", "plugins"}, slices.Collect(maps.Keys(out)))
				grpc := lookup(t, out, "plugins", "io.containerd.server.v1.grpc")
				assert.Equal(t, "/run/x.sock", grpc["address"])
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, migrateConfigFile(t, tc.config))
		})
	}
}

// migrateConfigFile returns the decoded output of migrateConfig for a config
// file with the given content.
func migrateConfigFile(t *testing.T, content string) map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	buf := bytes.NewBuffer(nil)
	require.NoError(t, migrateConfig(t.Context(), path, buf))

	first, _, _ := strings.Cut(buf.String(), "\n")
	assert.Equal(t, fmt.Sprintf("version = %d", version.ConfigVersion), first)

	var out map[string]any
	require.NoError(t, toml.Unmarshal(buf.Bytes(), &out), "output:\n%s", buf.String())
	return out
}

func TestMigrateConfigMissingFile(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	require.NoError(t, migrateConfig(t.Context(), filepath.Join(t.TempDir(), "config.toml"), buf))

	var out map[string]any
	require.NoError(t, toml.Unmarshal(buf.Bytes(), &out), "output:\n%s", buf.String())
	assert.Equal(t, map[string]any{"version": int64(version.ConfigVersion)}, out)
}

func TestMigrateConfigInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("version = \n"), 0600))

	buf := bytes.NewBuffer(nil)
	require.Error(t, migrateConfig(t.Context(), path, buf))
	assert.Empty(t, buf.String())
}

// TestMigrateConfigMapFields ensures migrateConfig knows all map fields of
// the config, values in map fields must not be removed from its output.
func TestMigrateConfigMapFields(t *testing.T) {
	var fields []string
	for f := range reflect.TypeFor[srvconfig.Config]().Fields() {
		if f.Type.Kind() == reflect.Map {
			name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
			fields = append(fields, name)
		}
	}
	assert.ElementsMatch(t, []string{"plugins", "proxy_plugins", "stream_processors", "timeouts"}, fields)
}

func lookup(t *testing.T, m map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		require.True(t, ok, "missing table %q in %v", k, m)
		m = next
	}
	return m
}

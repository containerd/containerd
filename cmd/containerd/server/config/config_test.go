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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/log/logtest"
)

func TestMigrations(t *testing.T) {
	if len(migrations) != version.ConfigVersion {
		t.Fatalf("Migration missing, expected %d migrations, only %d defined", version.ConfigVersion, len(migrations))
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

	t.Run("GlobRelativePath", func(t *testing.T) {
		imports, err := resolveImports(filepath.Join(tempDir, "root.toml"), []string{
			"config_*.toml", // Glob files from working dir
		})
		assert.NoError(t, err)
		assert.Equal(t, imports, []string{
			filepath.Join(tempDir, "config_1.toml"),
			filepath.Join(tempDir, "config_2.toml"),
		})
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

func TestLoadConfigWithV3GRPCImportsMergeSparseKeys(t *testing.T) {
	const (
		rootAddress   = "/tmp/root-containerd.sock"
		importAddress = "/tmp/import-containerd.sock"
		maxRecvSize   = 67108864
		maxSendSize   = 33554432
	)

	var (
		rootAddressConfig = fmt.Sprintf(`
version = 3
imports = ["data2.toml"]

[grpc]
  address = %q
`, rootAddress)

		rootMaxSendConfig = fmt.Sprintf(`
version = 3
imports = ["data2.toml"]

[grpc]
  max_send_message_size = %d
`, maxSendSize)

		rootAddressMaxSendConfig = fmt.Sprintf(`
version = 3
imports = ["data2.toml"]

[grpc]
  address = %q
  max_send_message_size = %d
`, rootAddress, maxSendSize)

		importAddressConfig = fmt.Sprintf(`
version = 3

[grpc]
  address = %q
`, importAddress)

		importMaxRecvConfig = fmt.Sprintf(`
version = 3

[grpc]
  max_recv_message_size = %d
`, maxRecvSize)

		importMaxSendConfig = fmt.Sprintf(`
version = 3

[grpc]
  max_send_message_size = %d
`, maxSendSize)
	)

	tests := []struct {
		name             string
		rootConfig       string
		importConfig     string
		wantAddress      string
		wantMaxRecvSize  int
		wantMaxSendSize  int
		wantTTRPCAddress string
	}{
		{
			name:             "import without address key does not overwrite root address",
			rootConfig:       rootAddressConfig,
			importConfig:     importMaxSendConfig,
			wantAddress:      rootAddress,
			wantMaxRecvSize:  defaults.DefaultMaxRecvMsgSize,
			wantMaxSendSize:  maxSendSize,
			wantTTRPCAddress: rootAddress + ".ttrpc",
		},
		{
			name:             "import address key is merged into root grpc config",
			rootConfig:       rootMaxSendConfig,
			importConfig:     importAddressConfig,
			wantAddress:      importAddress,
			wantMaxRecvSize:  defaults.DefaultMaxRecvMsgSize,
			wantMaxSendSize:  maxSendSize,
			wantTTRPCAddress: importAddress + ".ttrpc",
		},
		{
			name:             "import address key overwrites root address",
			rootConfig:       rootAddressMaxSendConfig,
			importConfig:     importAddressConfig,
			wantAddress:      importAddress,
			wantMaxRecvSize:  defaults.DefaultMaxRecvMsgSize,
			wantMaxSendSize:  maxSendSize,
			wantTTRPCAddress: importAddress + ".ttrpc",
		},
		{
			name:            "default address is kept when root and import omit address key",
			rootConfig:      rootMaxSendConfig,
			importConfig:    importMaxRecvConfig,
			wantAddress:     defaults.DefaultAddress,
			wantMaxRecvSize: maxRecvSize,
			wantMaxSendSize: maxSendSize,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()

			err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(tc.rootConfig), 0o600)
			require.NoError(t, err)

			err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(tc.importConfig), 0o600)
			require.NoError(t, err)

			out := Config{
				Version: version.ConfigVersion,
				Plugins: map[string]any{
					"io.containerd.server.v1.grpc": map[string]any{
						"address":               defaults.DefaultAddress,
						"max_recv_message_size": defaults.DefaultMaxRecvMsgSize,
						"max_send_message_size": defaults.DefaultMaxSendMsgSize,
					},
				},
			}
			err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
			require.NoError(t, err)

			grpcPlugin := out.Plugins["io.containerd.server.v1.grpc"].(map[string]any)
			require.Equal(t, tc.wantAddress, grpcPlugin["address"])
			require.Equal(t, tc.wantMaxRecvSize, grpcPlugin["max_recv_message_size"])
			require.Equal(t, tc.wantMaxSendSize, grpcPlugin["max_send_message_size"])

			if tc.wantTTRPCAddress != "" {
				ttrpcPlugin := out.Plugins["io.containerd.server.v1.ttrpc"].(map[string]any)
				require.Equal(t, tc.wantTTRPCAddress, ttrpcPlugin["address"])
			}
		})
	}
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

func TestLoadConfigWithImportsWithHigerVersion(t *testing.T) {
	data1 := `
version = 2
root = "/var/lib/containerd"
imports = ["data2.toml"]
`

	data2 := `
version = 3
disabled_plugins = ["io.containerd.v1.xyz"]
`
	tempDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0o600)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "data2.toml"), []byte(data2), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.Errorf(t, err, "drop-in config version 3 higher than root config version 2")
}

// https://github.com/containerd/containerd/issues/10905
func TestLoadConfigWithDefaultConfigVersion(t *testing.T) {
	data1 := `
disabled_plugins=["cri"]
`
	tempDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tempDir, "data1.toml"), []byte(data1), 0o600)
	assert.NoError(t, err)

	var out Config
	out.Version = version.ConfigVersion
	err = LoadConfig(context.Background(), filepath.Join(tempDir, "data1.toml"), &out)
	assert.NoError(t, err)

	assert.Equal(t, version.ConfigVersion, out.Version)
	assert.Equal(t, []string{"io.containerd.grpc.v1.cri"}, out.DisabledPlugins)
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

	pluginConfig := map[string]any{}
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
	assert.Equal(t, 4, out.Version)

	pluginConfig := map[string]any{}
	_, err = out.Decode(ctx, "io.containerd.runtime.v2.task", &pluginConfig)
	assert.NoError(t, err)
	assert.Equal(t, true, pluginConfig["shim_debug"])
}

func TestMergingPluginsWithTwoCriDropInConfigs(t *testing.T) {
	data1 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/cm/local/apps/kubernetes/current/bin/cni"
`
	data2 := `
[plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/cm/local/apps/containerd/var/etc/certs.d"
`
	expected := `
[cni]
  bin_dir = '/cm/local/apps/kubernetes/current/bin/cni'

[registry]
  config_path = '/cm/local/apps/containerd/var/etc/certs.d'
`

	testMergeConfig(t, []string{data1, data2}, expected, "io.containerd.grpc.v1.cri")
	testMergeConfig(t, []string{data2, data1}, expected, "io.containerd.grpc.v1.cri")
}

func TestMergingPluginsWithTwoCriCniDropInConfigs(t *testing.T) {
	data1 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    bin_dir = "/cm/local/apps/kubernetes/current/bin/cni"
`
	data2 := `
[plugins."io.containerd.grpc.v1.cri".cni]
    conf_dir = "/tmp"
`
	expected := `
[cni]
  bin_dir = '/cm/local/apps/kubernetes/current/bin/cni'
  conf_dir = '/tmp'
`
	testMergeConfig(t, []string{data1, data2}, expected, "io.containerd.grpc.v1.cri")
}

func TestMergingPluginsWithTwoCriRuntimeDropInConfigs(t *testing.T) {
	runcRuntime := `
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
    runtime_type = "io.containerd.runc.v2"
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
      SystemdCgroup = true
`
	nvidiaRuntime := `
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
	expected := `
[containerd]
  default_runtime_name = 'nvidia'

  [containerd.runtimes]
    [containerd.runtimes.nvidia]
      privileged_without_host_devices = false
      runtime_engine = ''
      runtime_root = ''
      runtime_type = 'io.containerd.runc.v2'

      [containerd.runtimes.nvidia.options]
        BinaryName = '/usr/bin/nvidia-container-runtime'
        SystemdCgroup = true

    [containerd.runtimes.runc]
      runtime_type = 'io.containerd.runc.v2'

      [containerd.runtimes.runc.options]
        SystemdCgroup = true
`
	testMergeConfig(t, []string{runcRuntime, nvidiaRuntime}, expected, "io.containerd.grpc.v1.cri")

	// Merging a third config that customizes only the default_runtime_name should result in mostly identical result
	runcDefault := `
    [plugins."io.containerd.grpc.v1.cri".containerd]
      default_runtime_name = "runc"
`
	// This will then be the only difference in our expected TOML
	expected2 := strings.Replace(expected, "default_runtime_name = 'nvidia'", "default_runtime_name = 'runc'", 1)

	testMergeConfig(t, []string{runcRuntime, nvidiaRuntime, runcDefault}, expected2, "io.containerd.grpc.v1.cri")

	// Mixing up the order will again result in 'nvidia' being the default runtime
	testMergeConfig(t, []string{runcRuntime, runcDefault, nvidiaRuntime}, expected, "io.containerd.grpc.v1.cri")
}

func TestMergingPluginsWithTwoCriRuntimeWithPodAnnotationsDropInConfigs(t *testing.T) {
	runc1 := `
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
    runtime_type = "io.containerd.runc.v2"
    cni_conf_dir = "/foo"
    pod_annotations = ["a", "b", "c"]
`
	runc2 := `
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
    runtime_type = "io.containerd.runc.v2"
    cni_conf_dir = "/bar"
    pod_annotations = ["d", "e", "f"]
`
	expected := `
[containerd]
  [containerd.runtimes]
    [containerd.runtimes.runc]
      cni_conf_dir = '/bar'
      pod_annotations = ['d', 'e', 'f']
      runtime_type = 'io.containerd.runc.v2'
`
	testMergeConfig(t, []string{runc1, runc2}, expected, "io.containerd.grpc.v1.cri")

	// The other way around: runc1 over runc2
	expected = `
[containerd]
  [containerd.runtimes]
    [containerd.runtimes.runc]
      cni_conf_dir = '/foo'
      pod_annotations = ['a', 'b', 'c']
      runtime_type = 'io.containerd.runc.v2'
`
	testMergeConfig(t, []string{runc2, runc1}, expected, "io.containerd.grpc.v1.cri")
}

func testMergeConfig(t *testing.T, inputs []string, expected string, comparePlugin string) {
	tempDir := t.TempDir()
	var result Config

	for i, data := range inputs {
		filename := fmt.Sprintf("data%d.toml", i+1)
		filepath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filepath, []byte(data), 0600)
		assert.NoError(t, err)

		var tempOut Config
		err = LoadConfig(context.Background(), filepath, &tempOut)
		assert.NoError(t, err)

		if i == 0 {
			result = tempOut
		} else {
			err = mergeConfig(&result, &tempOut)
			assert.NoError(t, err)
		}
	}

	criPlugin := result.Plugins[comparePlugin]
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).SetIndentTables(true).Encode(criPlugin); err != nil {
		panic(err)
	}
	assert.Equal(t, strings.TrimLeft(expected, "\n"), buf.String())
}

func TestServiceMigrate(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	t.Run("FullMigration", func(t *testing.T) {
		c := &Config{
			Debug: Debug{
				Address: "/run/containerd/debug.sock",
				UID:     1000,
				GID:     1000,
			},
			GRPC: GRPCConfig{
				Address:        "/run/containerd/containerd.sock",
				TCPAddress:     "127.0.0.1:1234",
				TCPTLSCA:       "/etc/ca.pem",
				TCPTLSCert:     "/etc/cert.pem",
				TCPTLSKey:      "/etc/key.pem",
				TCPTLSCName:    "containerd",
				UID:            0,
				GID:            0,
				MaxRecvMsgSize: 32 * 1024 * 1024,
				MaxSendMsgSize: 32 * 1024 * 1024,
			},
			TTRPC: TTRPCConfig{
				Address: "/run/containerd/containerd.sock.ttrpc",
				UID:     500,
				GID:     500,
			},
			Metrics: MetricsConfig{
				Address:       "127.0.0.1:9090",
				GRPCHistogram: true,
			},
		}
		err := serviceMigrate(ctx, c)
		assert.NoError(t, err)

		// Debug plugin
		debugPlugin := c.Plugins["io.containerd.server.v1.debug"].(map[string]any)
		assert.Equal(t, "/run/containerd/debug.sock", debugPlugin["address"])
		assert.Equal(t, 1000, debugPlugin["uid"])
		assert.Equal(t, 1000, debugPlugin["gid"])

		// GRPC plugin
		grpcPlugin := c.Plugins["io.containerd.server.v1.grpc"].(map[string]any)
		assert.Equal(t, "/run/containerd/containerd.sock", grpcPlugin["address"])
		assert.Equal(t, 0, grpcPlugin["uid"])
		assert.Equal(t, 0, grpcPlugin["gid"])
		assert.Equal(t, 32*1024*1024, grpcPlugin["max_recv_message_size"])
		assert.Equal(t, 32*1024*1024, grpcPlugin["max_send_message_size"])

		// GRPC TCP plugin
		tcpPlugin := c.Plugins["io.containerd.server.v1.grpc-tcp"].(map[string]any)
		assert.Equal(t, "127.0.0.1:1234", tcpPlugin["address"])
		assert.Equal(t, "/etc/ca.pem", tcpPlugin["tls_ca"])
		assert.Equal(t, "/etc/cert.pem", tcpPlugin["tls_cert"])
		assert.Equal(t, "/etc/key.pem", tcpPlugin["tls_key"])
		assert.Equal(t, "containerd", tcpPlugin["tls_common_name"])

		// TTRPC plugin
		ttrpcPlugin := c.Plugins["io.containerd.server.v1.ttrpc"].(map[string]any)
		assert.Equal(t, "/run/containerd/containerd.sock.ttrpc", ttrpcPlugin["address"])
		assert.Equal(t, 500, ttrpcPlugin["uid"])
		assert.Equal(t, 500, ttrpcPlugin["gid"])

		// Metrics plugin
		metricsPlugin := c.Plugins["io.containerd.server.v1.metrics"].(map[string]any)
		assert.Equal(t, "127.0.0.1:9090", metricsPlugin["address"])

		// GRPC prometheus plugin
		promPlugin := c.Plugins["io.containerd.metrics.v1.grpc-prometheus"].(map[string]any)
		assert.Equal(t, true, promPlugin["grpc_histogram"])

		// Legacy fields should be cleared
		assert.Empty(t, c.Debug.Address)
		assert.Zero(t, c.Debug.UID)
		assert.Zero(t, c.Debug.GID)
		assert.Empty(t, c.GRPC.Address)
		assert.Empty(t, c.GRPC.TCPAddress)
		assert.Empty(t, c.TTRPC.Address)
		assert.Empty(t, c.Metrics.Address)
		assert.False(t, c.Metrics.GRPCHistogram)
	})

	t.Run("TTRPCDefaultsToGRPC", func(t *testing.T) {
		c := &Config{
			GRPC: GRPCConfig{
				Address: "/run/containerd/containerd.sock",
				UID:     1000,
				GID:     1000,
			},
		}
		err := serviceMigrate(ctx, c)
		assert.NoError(t, err)

		ttrpcPlugin := c.Plugins["io.containerd.server.v1.ttrpc"].(map[string]any)
		assert.Equal(t, "/run/containerd/containerd.sock.ttrpc", ttrpcPlugin["address"])
		assert.Equal(t, 1000, ttrpcPlugin["uid"])
		assert.Equal(t, 1000, ttrpcPlugin["gid"])
	})

	t.Run("TTRPCExplicitUIDGIDNotOverridden", func(t *testing.T) {
		c := &Config{
			GRPC: GRPCConfig{
				Address: "/run/containerd/containerd.sock",
				UID:     1000,
				GID:     1000,
			},
			TTRPC: TTRPCConfig{
				UID: 500,
				GID: 500,
			},
		}
		err := serviceMigrate(ctx, c)
		assert.NoError(t, err)

		ttrpcPlugin := c.Plugins["io.containerd.server.v1.ttrpc"].(map[string]any)
		assert.Equal(t, "/run/containerd/containerd.sock.ttrpc", ttrpcPlugin["address"])
		assert.Equal(t, 500, ttrpcPlugin["uid"])
		assert.Equal(t, 500, ttrpcPlugin["gid"])
	})

	t.Run("ExistingPluginConfigNotOverwritten", func(t *testing.T) {
		c := &Config{
			Plugins: map[string]any{
				"io.containerd.server.v1.grpc": map[string]any{
					"address": "/custom/path.sock",
				},
			},
			GRPC: GRPCConfig{
				Address: "/run/containerd/containerd.sock",
			},
		}
		err := serviceMigrate(ctx, c)
		assert.NoError(t, err)

		// Existing plugin config should be preserved
		grpcPlugin := c.Plugins["io.containerd.server.v1.grpc"].(map[string]any)
		assert.Equal(t, "/custom/path.sock", grpcPlugin["address"])
	})

	t.Run("EmptyConfig", func(t *testing.T) {
		c := &Config{}
		err := serviceMigrate(ctx, c)
		assert.NoError(t, err)

		// No plugins should be created for empty config
		assert.Nil(t, c.Plugins["io.containerd.server.v1.debug"])
		assert.Nil(t, c.Plugins["io.containerd.server.v1.grpc"])
		assert.Nil(t, c.Plugins["io.containerd.server.v1.grpc-tcp"])
		assert.Nil(t, c.Plugins["io.containerd.server.v1.ttrpc"])
		assert.Nil(t, c.Plugins["io.containerd.server.v1.metrics"])
	})
}

func TestV3MigrateTTRPCDerivedFromGRPC(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	data := `
version = 3

[grpc]
  address = "/custom/containerd.sock"
  uid = 1000
  gid = 1000
`
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(data), 0o600)
	assert.NoError(t, err)

	var out Config
	err = LoadConfig(context.Background(), path, &out)
	assert.NoError(t, err)
	assert.Equal(t, 3, out.Version)

	err = out.MigrateConfig(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 4, out.Version)

	// TTRPC plugin should be created with address derived from GRPC
	ttrpcPlugin := out.Plugins["io.containerd.server.v1.ttrpc"].(map[string]any)
	assert.Equal(t, "/custom/containerd.sock.ttrpc", ttrpcPlugin["address"])
	assert.Equal(t, 1000, ttrpcPlugin["uid"])
	assert.Equal(t, 1000, ttrpcPlugin["gid"])

	// GRPC plugin should have the configured address
	grpcPlugin := out.Plugins["io.containerd.server.v1.grpc"].(map[string]any)
	assert.Equal(t, "/custom/containerd.sock", grpcPlugin["address"])

	// Legacy fields should be cleared
	assert.Empty(t, out.GRPC.Address)
	assert.Empty(t, out.TTRPC.Address)
}

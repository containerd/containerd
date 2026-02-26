package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestDumpConfigWithPluginMigration(t *testing.T) {
	// Reset registry to ensure clean state
	registry.Reset()
	defer registry.Reset()

	// Register a test plugin with config migration
	registry.Register(&plugin.Registration{
		Type: plugins.CRIServicePlugin,
		ID:   "runtime",
		ConfigMigration: func(ctx context.Context, configVersion int, pluginConfigs map[string]interface{}) error {
			// This simulates the CRI plugin migration from v1 to v2
			if configVersion >= 3 {
				return nil
			}
			
			// Check if we have the old plugin config
			src, ok := pluginConfigs["io.containerd.grpc.v1.cri"]
			if !ok {
				return nil
			}
			
			// Migrate the config
			srcMap := src.(map[string]interface{})
			if cniConfig, ok := srcMap["cni"].(map[string]interface{}); ok {
				if binDir, ok := cniConfig["bin_dir"].(string); ok {
					// Create new plugin config if it doesn't exist
					dst, ok := pluginConfigs["io.containerd.cri.v1.runtime"]
					if !ok {
						dst = make(map[string]interface{})
					}
					dstMap := dst.(map[string]interface{})
					
					// Create cni config in new plugin
					newCniConfig := make(map[string]interface{})
					if existingCni, ok := dstMap["cni"].(map[string]interface{}); ok {
						newCniConfig = existingCni
					}
					
					// Migrate bin_dir to bin_dirs
					if binDirs, ok := newCniConfig["bin_dirs"].([]string); !ok || len(binDirs) == 0 {
						newCniConfig["bin_dirs"] = []string{binDir}
					}
					
					// Copy other cni config values
					for k, v := range cniConfig {
						if k != "bin_dir" {
							if _, ok := newCniConfig[k]; !ok {
								newCniConfig[k] = v
							}
						}
					}
					
					dstMap["cni"] = newCniConfig
					pluginConfigs["io.containerd.cri.v1.runtime"] = dstMap
				}
			}
			
			return nil
		},
	})

	tests := []struct {
		name           string
		configContent  string
		expectedOutput string
		shouldContain  []string
		shouldNotContain []string
	}{
		{
			name: "Version 2 config with plugin migration",
			configContent: `version = 2
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`,
			shouldContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
				`conf_dir = "/etc/cni/net.d"`,
				`conf_template = ""`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
			},
		},
		{
			name: "Version 3 config should not trigger migration",
			configContent: `version = 3
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
`,
			shouldContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.toml")
			err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
			require.NoError(t, err)

			// Capture stdout
			var buf bytes.Buffer
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w
			defer func() {
				os.Stdout = oldStdout
			}()

			app := cli.NewApp()
			app.Name = "containerd"
			app.Flags = []cli.Flag{
				&cli.StringFlag{
					Name:    "config",
					Aliases: []string{"c"},
					Usage:   "Path to the configuration file",
					Value:   filepath.Join(defaults.DefaultConfigDir, "config.toml"),
				},
			}
			app.Commands = []*cli.Command{configCommand}
			
			err = app.Run([]string{"./bin/containerd", "--config", configPath, "config", "dump"})
			require.NoError(t, err)

			w.Close()
			_, err = buf.ReadFrom(r)
			require.NoError(t, err)
			output := buf.String()

			for _, expected := range tt.shouldContain {
				assert.Contains(t, output, expected, "Output should contain: %s", expected)
			}

			for _, unexpected := range tt.shouldNotContain {
				assert.NotContains(t, output, unexpected, "Output should not contain: %s", unexpected)
			}
		})
	}
}

func TestDumpConfigWithoutPluginMigration(t *testing.T) {
	registry.Reset()
	defer registry.Reset()

	// Create a version 2 config without any plugin migrations
	configContent := `version = 2
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	app := cli.NewApp()
	app.Name = "containerd"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the configuration file",
			Value:   filepath.Join(defaults.DefaultConfigDir, "config.toml"),
		},
	}
	app.Commands = []*cli.Command{configCommand}
	
	err = app.Run([]string{"./bin/containerd", "--config", configPath, "config", "dump"})
	require.NoError(t, err)

	w.Close()
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	output := buf.String()

	// Without plugin migrations, the config should remain unchanged
	assert.Contains(t, output, `[plugins."io.containerd.grpc.v1.cri".cni]`)
	assert.Contains(t, output, `bin_dir = "/home/kubernetes/bin"`)
	assert.NotContains(t, output, `[plugins."io.containerd.cri.v1.runtime".cni]`)
	assert.NotContains(t, output, `bin_dirs = ["/home/kubernetes/bin"]`)
}

func TestDumpConfigWithDefaultValues(t *testing.T) {
	// Test that dumpConfig doesn't fill in default values unnecessarily
	configContent := `version = 2
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Capture stdout
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	app := cli.NewApp()
	app.Name = "containerd"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the configuration file",
			Value:   filepath.Join(defaults.DefaultConfigDir, "config.toml"),
		},
	}
	app.Commands = []*cli.Command{configCommand}
	
	err = app.Run([]string{"./bin/containerd", "--config", configPath, "config", "dump"})
	require.NoError(t, err)

	w.Close()
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	output := buf.String()

	// The output should contain the user's values
	assert.Contains(t, output, `bin_dir = "/home/kubernetes/bin"`)
	assert.Contains(t, output, `conf_dir = "/etc/cni/net.d"`)
	
	// It should NOT contain default values that weren't in the original config
	assert.NotContains(t, output, `max_conf_num = 1`)
	assert.NotContains(t, output, `setup_serially = false`)
	assert.NotContains(t, output, `ip_pref = ""`)
	assert.NotContains(t, output, `use_internal_loopback = false`)
}

func TestDumpConfig_VersionCases(t *testing.T) {
	registry.Reset()
	defer registry.Reset()

	registry.Register(&plugin.Registration{
		Type: plugins.CRIServicePlugin,
		ID:   "runtime",
		ConfigMigration: func(ctx context.Context, configVersion int, pluginConfigs map[string]interface{}) error {
			if configVersion >= 3 {
				return nil
			}
			src, ok := pluginConfigs["io.containerd.grpc.v1.cri"]
			if !ok {
				return nil
			}
			srcMap := src.(map[string]interface{})
			if cniConfig, ok := srcMap["cni"].(map[string]interface{}); ok {
				if binDir, ok := cniConfig["bin_dir"].(string); ok {
					dst, ok := pluginConfigs["io.containerd.cri.v1.runtime"]
					if !ok {
						dst = make(map[string]interface{})
					}
					dstMap := dst.(map[string]interface{})
					newCniConfig := make(map[string]interface{})
					if existingCni, ok := dstMap["cni"].(map[string]interface{}); ok {
						newCniConfig = existingCni
					}
					if binDirs, ok := newCniConfig["bin_dirs"].([]string); !ok || len(binDirs) == 0 {
						newCniConfig["bin_dirs"] = []string{binDir}
					}
					for k, v := range cniConfig {
						if k != "bin_dir" {
							if _, ok := newCniConfig[k]; !ok {
								newCniConfig[k] = v
							}
						}
					}
					dstMap["cni"] = newCniConfig
					pluginConfigs["io.containerd.cri.v1.runtime"] = dstMap
				}
			}
			return nil
		},
	})

	tests := []struct {
		name          string
		configContent string
		shouldContain []string
		shouldNotContain []string
	}{
		{
			name: "Unspecified version",
			configContent: `
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`,
			shouldContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
				`conf_dir = "/etc/cni/net.d"`,
				`conf_template = ""`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
			},
		},
		{
			name: "Version 1",
			configContent: `version = 1
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`,
			shouldContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
				`conf_dir = "/etc/cni/net.d"`,
				`conf_template = ""`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
			},
		},
		{
			name: "Version 2",
			configContent: `version = 2
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`,
			shouldContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
				`conf_dir = "/etc/cni/net.d"`,
				`conf_template = ""`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
			},
		},
		{
			name: "Version 3",
			configContent: `version = 3
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`,
			shouldContain: []string{
				`[plugins."io.containerd.grpc.v1.cri".cni]`,
				`bin_dir = "/home/kubernetes/bin"`,
				`conf_dir = "/etc/cni/net.d"`,
				`conf_template = ""`,
			},
			shouldNotContain: []string{
				`[plugins."io.containerd.cri.v1.runtime".cni]`,
				`bin_dirs = ["/home/kubernetes/bin"]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.toml")
			err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
			require.NoError(t, err)

			var buf bytes.Buffer
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w
			defer func() {
				os.Stdout = oldStdout
			}()

			app := cli.NewApp()
			app.Name = "containerd"
			app.Flags = []cli.Flag{
				&cli.StringFlag{
					Name:    "config",
					Aliases: []string{"c"},
					Usage:   "Path to the configuration file",
					Value:   filepath.Join(defaults.DefaultConfigDir, "config.toml"),
				},
			}
			app.Commands = []*cli.Command{configCommand}
			err = app.Run([]string{"./bin/containerd", "--config", configPath, "config", "dump"})
			require.NoError(t, err)

			w.Close()
			_, err = buf.ReadFrom(r)
			require.NoError(t, err)
			output := buf.String()

			for _, expected := range tt.shouldContain {
				assert.Contains(t, output, expected, "Output should contain: %s", expected)
			}
			for _, unexpected := range tt.shouldNotContain {
				assert.NotContains(t, output, unexpected, "Output should not contain: %s", unexpected)
			}
		})
	}
} 
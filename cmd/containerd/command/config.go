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
	"context"
	"os"
	"path/filepath"
	"reflect"

	"github.com/containerd/containerd/v2/cmd/containerd/server"
	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v2"
)

func outputConfig(ctx context.Context, config *srvconfig.Config) error {
	plugins, err := server.LoadPlugins(ctx, config)
	if err != nil {
		return err
	}
	if len(plugins) != 0 {
		if config.Plugins == nil {
			config.Plugins = make(map[string]interface{})
		}
		for _, p := range plugins {
			if p.Config == nil {
				continue
			}

			pc, err := config.Decode(ctx, p.URI(), p.Config)
			if err != nil {
				return err
			}

			config.Plugins[p.URI()] = pc
		}
	}

	if config.Timeouts == nil {
		config.Timeouts = make(map[string]string)
	}
	timeouts := timeout.All()
	for k, v := range timeouts {
		if config.Timeouts[k] == "" {
			config.Timeouts[k] = v.String()
		}
	}

	// for the time being, keep the defaultConfig's version set at 1 so that
	// when a config without a version is loaded from disk and has no version
	// set, we assume it's a v1 config.  But when generating new configs via
	// this command, generate the max configuration version
	config.Version = version.ConfigVersion

	return toml.NewEncoder(os.Stdout).SetIndentTables(true).Encode(config)
}

func defaultConfig() *srvconfig.Config {
	return platformAgnosticDefaultConfig()
}

var configCommand = &cli.Command{
	Name:  "config",
	Usage: "Information on the containerd config",
	Subcommands: []*cli.Command{
		{
			Name:  "default",
			Usage: "See the output of the default config",
			Action: func(cliContext *cli.Context) error {
				ctx := cliContext.Context
				return outputConfig(ctx, defaultConfig())
			},
		},
		{
			Name:   "dump",
			Usage:  "See the output of the final main config with imported in subconfig files",
			Action: dumpConfig,
		},
		{
			Name:   "migrate",
			Usage:  "Migrate the current configuration file to the latest version (does not migrate subconfig files)",
			Action: migrateConfig,
		},
	},
}

func dumpConfig(cliContext *cli.Context) error {
	config := defaultConfig()
	ctx := cliContext.Context
	if err := srvconfig.LoadConfig(ctx, cliContext.String("config"), config); err != nil && !os.IsNotExist(err) {
		return err
	}

	if config.Version < version.ConfigVersion {
		plugins := registry.Graph(srvconfig.V2DisabledFilter(config.DisabledPlugins))
		for _, p := range plugins {
			if p.ConfigMigration != nil {
				if err := p.ConfigMigration(ctx, config.Version, config.Plugins); err != nil {
					return err
				}
			}
		}
	}
	return outputConfig(ctx, config)
}

func migrateConfig(cliContext *cli.Context) error {
	config := defaultConfig()
	ctx := cliContext.Context
	configPath := cliContext.String("config")

	if err := srvconfig.LoadConfig(ctx, configPath, config); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Run plugin migrations
	if config.Version < version.ConfigVersion {
		plugins := registry.Graph(srvconfig.V2DisabledFilter(config.DisabledPlugins))
		for _, p := range plugins {
			if p.ConfigMigration != nil {
				if err := p.ConfigMigration(ctx, config.Version, config.Plugins); err != nil {
					return err
				}
			}
		}
	}

	// For migrate, we want to show only the user's config values plus any migrated values,
	// preserving all explicitly set keys even if they have default values
	return outputMigrateConfig(ctx, config, configPath)
}

// parseOriginalKeys parses the original TOML file and returns a set of keys that were explicitly set.
func parseOriginalKeys(configPath string) (map[string]bool, map[string]bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, err
	}

	// Parse the TOML file to track which keys were present
	var config map[string]interface{}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, nil, err
	}

	// Extract all top-level keys
	keys := make(map[string]bool)
	for key := range config {
		keys[key] = true
	}

	// Extract plugin keys that were explicitly set
	pluginKeys := make(map[string]bool)
	if plugins, exists := config["plugins"]; exists {
		if pluginsMap, ok := plugins.(map[string]interface{}); ok {
			for pluginKey := range pluginsMap {
				pluginKeys[pluginKey] = true
			}
		}
	}

	return keys, pluginKeys, nil
}

// Helper functions to check if config sections have non-default values.
func hasNonDefaultGRPC(config *srvconfig.Config) bool {
	defaultConfig := platformAgnosticDefaultConfig()
	return config.GRPC.Address != defaultConfig.GRPC.Address ||
		config.GRPC.TCPAddress != defaultConfig.GRPC.TCPAddress ||
		config.GRPC.TCPTLSCA != defaultConfig.GRPC.TCPTLSCA ||
		config.GRPC.TCPTLSCert != defaultConfig.GRPC.TCPTLSCert ||
		config.GRPC.TCPTLSKey != defaultConfig.GRPC.TCPTLSKey ||
		config.GRPC.UID != defaultConfig.GRPC.UID ||
		config.GRPC.GID != defaultConfig.GRPC.GID ||
		config.GRPC.MaxRecvMsgSize != defaultConfig.GRPC.MaxRecvMsgSize ||
		config.GRPC.MaxSendMsgSize != defaultConfig.GRPC.MaxSendMsgSize
}

func hasNonDefaultTTRPC(config *srvconfig.Config) bool {
	defaultConfig := platformAgnosticDefaultConfig()
	return config.TTRPC.Address != defaultConfig.TTRPC.Address ||
		config.TTRPC.UID != defaultConfig.TTRPC.UID ||
		config.TTRPC.GID != defaultConfig.TTRPC.GID
}

func hasNonDefaultDebug(config *srvconfig.Config) bool {
	defaultConfig := platformAgnosticDefaultConfig()
	return config.Debug.Address != defaultConfig.Debug.Address ||
		config.Debug.UID != defaultConfig.Debug.UID ||
		config.Debug.GID != defaultConfig.Debug.GID ||
		config.Debug.Level != defaultConfig.Debug.Level ||
		config.Debug.Format != defaultConfig.Debug.Format
}

func hasNonDefaultMetrics(config *srvconfig.Config) bool {
	defaultConfig := platformAgnosticDefaultConfig()
	return config.Metrics.Address != defaultConfig.Metrics.Address ||
		config.Metrics.GRPCHistogram != defaultConfig.Metrics.GRPCHistogram
}

// isEmptyPluginConfig checks if a plugin config is empty.
func isEmptyPluginConfig(pluginConfig interface{}) bool {
	if pluginConfigMap, ok := pluginConfig.(map[string]interface{}); ok {
		return len(pluginConfigMap) == 0
	}
	return true
}

// hasNonDefaultPluginConfig checks if a plugin config has non-default values.
func hasNonDefaultPluginConfig(pluginID string, pluginConfig interface{}) bool {
	// Get the default config for this plugin
	plugins := registry.Graph(srvconfig.V2DisabledFilter(nil)) // Use proper filter
	for _, p := range plugins {
		if p.URI() == pluginID && p.Config != nil {
			// Compare the plugin config with its default
			return !reflect.DeepEqual(pluginConfig, p.Config)
		}
	}

	// If we can't find the plugin or it has no default config,
	// assume it has non-default values if it's not empty
	if pluginConfigMap, ok := pluginConfig.(map[string]interface{}); ok {
		return len(pluginConfigMap) > 0
	}

	return true
}

func platformAgnosticDefaultConfig() *srvconfig.Config {
	return &srvconfig.Config{
		Version: version.ConfigVersion,
		Root:    defaults.DefaultRootDir,
		State:   defaults.DefaultStateDir,
		GRPC: srvconfig.GRPCConfig{
			Address:        defaults.DefaultAddress,
			MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
			MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
		},
		DisabledPlugins:  []string{},
		RequiredPlugins:  []string{},
		StreamProcessors: streamProcessors(),
		Imports:          []string{defaults.DefaultConfigIncludePattern},
	}
}

func streamProcessors() map[string]srvconfig.StreamProcessor {
	const (
		ctdDecoder = "ctd-decoder"
		basename   = "io.containerd.ocicrypt.decoder.v1"
	)
	decryptionKeysPath := filepath.Join(defaults.DefaultConfigDir, "ocicrypt", "keys")
	ctdDecoderArgs := []string{
		"--decryption-keys-path", decryptionKeysPath,
	}
	ctdDecoderEnv := []string{
		"OCICRYPT_KEYPROVIDER_CONFIG=" + filepath.Join(defaults.DefaultConfigDir, "ocicrypt", "ocicrypt_keyprovider.conf"),
	}
	return map[string]srvconfig.StreamProcessor{
		basename + ".tar.gzip": {
			Accepts: []string{images.MediaTypeImageLayerGzipEncrypted},
			Returns: ocispec.MediaTypeImageLayerGzip,
			Path:    ctdDecoder,
			Args:    ctdDecoderArgs,
			Env:     ctdDecoderEnv,
		},
		basename + ".tar": {
			Accepts: []string{images.MediaTypeImageLayerEncrypted},
			Returns: ocispec.MediaTypeImageLayer,
			Path:    ctdDecoder,
			Args:    ctdDecoderArgs,
			Env:     ctdDecoderEnv,
		},
	}
}

// outputMigrateConfig outputs only the user's configuration values plus any migrated values,
// preserving all explicitly set keys even if those keys have the default values.
func outputMigrateConfig(ctx context.Context, configWithDefaults *srvconfig.Config, configPath string) error {
	// Load the original config file to track which keys were explicitly set
	originalKeys := make(map[string]bool)
	originalPluginKeys := make(map[string]bool)

	if configPath != "" {
		if keys, pluginKeys, err := parseOriginalKeys(configPath); err == nil {
			originalKeys = keys
			originalPluginKeys = pluginKeys
		}
	}

	if configWithDefaults.Version < version.ConfigVersion {
		plugins := registry.Graph(srvconfig.V2DisabledFilter(configWithDefaults.DisabledPlugins))
		for _, p := range plugins {
			if p.ConfigMigration != nil {
				if err := p.ConfigMigration(ctx, configWithDefaults.Version, configWithDefaults.Plugins); err != nil {
					return err
				}
			}
		}
	}

	minimalConfig := make(map[string]interface{})

	minimalConfig["version"] = version.ConfigVersion // always include version ?

	// Only include keys that were explicitly set in the original config
	if originalKeys["root"] || configWithDefaults.Root != "" {
		minimalConfig["root"] = configWithDefaults.Root
	}
	if originalKeys["state"] || configWithDefaults.State != "" {
		minimalConfig["state"] = configWithDefaults.State
	}
	if originalKeys["temp"] || configWithDefaults.TempDir != "" {
		minimalConfig["temp"] = configWithDefaults.TempDir
	}
	if originalKeys["disabled_plugins"] || len(configWithDefaults.DisabledPlugins) > 0 {
		minimalConfig["disabled_plugins"] = configWithDefaults.DisabledPlugins
	}
	if originalKeys["required_plugins"] || len(configWithDefaults.RequiredPlugins) > 0 {
		minimalConfig["required_plugins"] = configWithDefaults.RequiredPlugins
	}
	if originalKeys["oom_score"] {
		minimalConfig["oom_score"] = configWithDefaults.OOMScore
	}
	if originalKeys["imports"] || len(configWithDefaults.Imports) > 0 {
		minimalConfig["imports"] = configWithDefaults.Imports
	}

	// Only include GRPC if it was explicitly set or has non-default values
	if originalKeys["grpc"] || hasNonDefaultGRPC(configWithDefaults) {
		minimalConfig["grpc"] = configWithDefaults.GRPC
	}

	// Only include TTRPC if it was explicitly set or has non-default values
	if originalKeys["ttrpc"] || hasNonDefaultTTRPC(configWithDefaults) {
		minimalConfig["ttrpc"] = configWithDefaults.TTRPC
	}

	// Only include Debug if it was explicitly set or has non-default values
	if originalKeys["debug"] || hasNonDefaultDebug(configWithDefaults) {
		minimalConfig["debug"] = configWithDefaults.Debug
	}

	// Only include Metrics if it was explicitly set or has non-default values
	if originalKeys["metrics"] || hasNonDefaultMetrics(configWithDefaults) {
		minimalConfig["metrics"] = configWithDefaults.Metrics
	}

	// Only include Plugins that were explicitly set in the original config or created by migrations
	if len(configWithDefaults.Plugins) > 0 {
		filteredPlugins := make(map[string]interface{})
		for pluginID, pluginConfig := range configWithDefaults.Plugins {
			// preserve plugins that were in the original config
			if originalPluginKeys[pluginID] {
				// iff they have actual content
				if !isEmptyPluginConfig(pluginConfig) {
					filteredPlugins[pluginID] = pluginConfig
				}
			} else {
				// For plugins not in the original config, only include them if they have non-default values
				// This should handle plugins created by migrations
				if hasNonDefaultPluginConfig(pluginID, pluginConfig) && !isEmptyPluginConfig(pluginConfig) {
					filteredPlugins[pluginID] = pluginConfig
				}
			}
		}
		if len(filteredPlugins) > 0 {
			minimalConfig["plugins"] = filteredPlugins
		}
	}

	// Only include CGroup if it was explicitly set or has non-default values
	if originalKeys["cgroup"] || configWithDefaults.Cgroup.Path != "" {
		minimalConfig["cgroup"] = configWithDefaults.Cgroup
	}

	// Only include StreamProcessors if they were explicitly set in the original config
	if originalKeys["stream_processors"] {
		minimalConfig["stream_processors"] = configWithDefaults.StreamProcessors
	}

	// Only include Timeouts if it was explicitly set or has any values
	if originalKeys["timeouts"] || len(configWithDefaults.Timeouts) > 0 {
		minimalConfig["timeouts"] = configWithDefaults.Timeouts
	}

	return toml.NewEncoder(os.Stdout).SetIndentTables(true).Encode(minimalConfig)
}

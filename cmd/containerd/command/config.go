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
	if err := srvconfig.LoadConfig(ctx, cliContext.String("config"), config); err != nil && !os.IsNotExist(err) {
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
	// without filling in default values that weren't in the original config
	return outputMigrateConfig(ctx, config)
}

// outputMigrateConfig outputs only the user's configuration values plus any migrated values,
// without filling in default values that weren't in the original config.
func outputMigrateConfig(_ context.Context, config *srvconfig.Config) error {
	plugins := registry.Graph(srvconfig.V2DisabledFilter(config.DisabledPlugins))

	// store default configs from plugins
	defaultConfigs := make(map[string]interface{})
	for _, p := range plugins {
		if p.Config != nil {
			// hack: marshal and unmarshal to get a clean copy of the default config
			defaultData, err := toml.Marshal(p.Config)
			if err != nil {
				return err
			}
			var defaultConfig interface{}
			if err := toml.Unmarshal(defaultData, &defaultConfig); err != nil {
				return err
			}
			defaultConfigs[p.URI()] = defaultConfig
		}
	}

	// process each plugin config to remove default values that weren't in the original
	if config.Plugins != nil {
		for pluginID, pluginConfig := range config.Plugins {
			if defaultConfig, exists := defaultConfigs[pluginID]; exists {
				// remove default values that weren't in the original config
				// NOTE: this will remove a key if its values is the same as the default value
				// that can be an acceptable limitation for now.
				filteredConfig := removeDefaultValues(pluginConfig, defaultConfig)
				if filteredConfig != nil {
					config.Plugins[pluginID] = filteredConfig
				} else {
					delete(config.Plugins, pluginID)
				}
			}
		}
	}

	if config.Timeouts != nil {
		allTimeouts := timeout.All()
		for k := range config.Timeouts {
			if defaultTimeout, exists := allTimeouts[k]; exists {
				if config.Timeouts[k] == defaultTimeout.String() {
					delete(config.Timeouts, k)
				}
			}
		}

		if len(config.Timeouts) == 0 {
			config.Timeouts = nil
		}
	}

	config.Version = version.ConfigVersion

	return toml.NewEncoder(os.Stdout).SetIndentTables(true).Encode(config)
}

// removeDefaultValues recursively removes values from config that match the defaults.
func removeDefaultValues(config, defaults interface{}) interface{} {
	if config == nil || defaults == nil {
		return config
	}

	switch configVal := config.(type) {
	case map[string]interface{}:
		defaultsMap, ok := defaults.(map[string]interface{})
		if !ok {
			return config
		}

		result := make(map[string]interface{})
		for k, v := range configVal {
			if defaultVal, exists := defaultsMap[k]; exists {
				if processed := removeDefaultValues(v, defaultVal); processed != nil {
					result[k] = processed
				}
			} else {
				// this key doesn't exist in defaults, keep it
				result[k] = v
			}
		}

		if len(result) == 0 {
			return nil
		}
		return result

	case []interface{}:
		defaultsSlice, ok := defaults.([]interface{})
		if !ok {
			return config
		}

		if reflect.DeepEqual(configVal, defaultsSlice) {
			return nil
		}
		return configVal

	default:
		if reflect.DeepEqual(config, defaults) {
			return nil
		}
		return config
	}
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

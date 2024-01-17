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
	gocontext "context"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/cmd/containerd/server"
	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli"
)

func outputConfig(ctx gocontext.Context, config *srvconfig.Config) error {
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
	config.Version = srvconfig.CurrentConfigVersion

	return toml.NewEncoder(os.Stdout).SetIndentTables(true).Encode(config)
}

func defaultConfig() *srvconfig.Config {
	return platformAgnosticDefaultConfig()
}

var configCommand = cli.Command{
	Name:  "config",
	Usage: "Information on the containerd config",
	Subcommands: []cli.Command{
		{
			Name:  "default",
			Usage: "See the output of the default config",
			Action: func(context *cli.Context) error {
				return outputConfig(gocontext.Background(), defaultConfig())
			},
		},
		{
			Name:  "dump",
			Usage: "See the output of the final main config with imported in subconfig files",
			Action: func(context *cli.Context) error {
				config := defaultConfig()
				ctx := gocontext.Background()
				if err := srvconfig.LoadConfig(ctx, context.GlobalString("config"), config); err != nil && !os.IsNotExist(err) {
					return err
				}

				return outputConfig(ctx, config)
			},
		},
		{
			Name:  "migrate",
			Usage: "Migrate the current configuration file to the latest version (does not migrate subconfig files)",
			Action: func(context *cli.Context) error {
				config := defaultConfig()
				ctx := gocontext.Background()
				if err := srvconfig.LoadConfig(ctx, context.GlobalString("config"), config); err != nil && !os.IsNotExist(err) {
					return err
				}

				if config.Version < srvconfig.CurrentConfigVersion {
					plugins := registry.Graph(srvconfig.V2DisabledFilter(config.DisabledPlugins))
					for _, p := range plugins {
						if p.ConfigMigration != nil {
							if err := p.ConfigMigration(ctx, config.Version, config.Plugins); err != nil {
								return err
							}
						}
					}
				}

				config.Version = srvconfig.CurrentConfigVersion

				return toml.NewEncoder(os.Stdout).SetIndentTables(true).Encode(config)
			},
		},
	},
}

func platformAgnosticDefaultConfig() *srvconfig.Config {
	return &srvconfig.Config{
		Version: srvconfig.CurrentConfigVersion,
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

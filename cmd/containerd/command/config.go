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
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"slices"

	"github.com/containerd/containerd/v2/cmd/containerd/server"
	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v3"
)

func outputConfig(ctx context.Context, config *srvconfig.Config) error {
	plugins, err := server.LoadPlugins(ctx, config)
	if err != nil {
		return err
	}
	if len(plugins) != 0 {
		if config.Plugins == nil {
			config.Plugins = make(map[string]any)
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
	Commands: []*cli.Command{
		{
			Name:  "default",
			Usage: "See the output of the default config",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return outputConfig(ctx, defaultConfig())
			},
		},
		{
			Name:   "dump",
			Usage:  "See the output of the final main config with imported in subconfig files",
			Action: dumpConfig,
		},
		{
			Name:  "migrate",
			Usage: "Migrate the current configuration file to the latest version (does not migrate subconfig files)",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				return migrateConfig(ctx, cmd.String("config"), os.Stdout)
			},
		},
	},
}

func dumpConfig(ctx context.Context, cmd *cli.Command) error {
	config := defaultConfig()
	if err := loadConfig(ctx, cmd.String("config"), config); err != nil {
		return err
	}

	return outputConfig(ctx, config)
}

// loadConfig loads the config file at path into config, running the config
// migrations of all registered plugins. A missing file is not an error.
func loadConfig(ctx context.Context, path string, config *srvconfig.Config) error {
	g := registry.Graph(func(*plugin.Registration) bool { return false })
	plugins := func() iter.Seq[plugin.Registration] {
		return slices.Values(g)
	}
	if err := srvconfig.LoadConfigWithPlugins(ctx, path, plugins, config); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// migrateConfig writes the config file at path migrated to the latest
// version. Only values set in the file and the files it imports are output,
// no defaults are added.
func migrateConfig(ctx context.Context, path string, w io.Writer) error {
	// The version must be set to the latest, a loaded config is only
	// migrated up to the version of the config it is loaded into.
	config := &srvconfig.Config{Version: version.ConfigVersion}
	if err := loadConfig(ctx, path, config); err != nil {
		return err
	}

	b, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	out := map[string]any{}
	if err := toml.Unmarshal(b, &out); err != nil {
		return err
	}

	// The config fields are encoded even when unset. Unset values and empty
	// values have the same meaning when a config is loaded, so empty values
	// can be removed. This does not apply to the map fields. The entries of
	// proxy_plugins, stream_processors and timeouts replace existing entries
	// as a whole when loaded, and plugin configs are decoded on top of the
	// plugin defaults, where an empty value overrides a default.
	for k, v := range out {
		switch k {
		case "plugins", "proxy_plugins", "stream_processors", "timeouts":
			if m, ok := v.(map[string]any); ok && len(m) == 0 {
				delete(out, k)
			}
		default:
			if isEmptyValue(v) {
				delete(out, k)
			}
		}
	}

	// The version is written separately to keep it first in the output.
	delete(out, "version")
	if _, err := fmt.Fprintf(w, "version = %d\n", config.Version); err != nil {
		return err
	}
	if len(out) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return toml.NewEncoder(w).SetIndentTables(true).Encode(out)
}

// isEmptyValue returns whether a decoded TOML value is empty, a table is
// empty when it has no non-empty values. Empty values are removed from tables.
func isEmptyValue(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			if isEmptyValue(e) {
				delete(v, k)
			}
		}
		return len(v) == 0
	case []any:
		return len(v) == 0
	case string:
		return v == ""
	case int64:
		return v == 0
	case bool:
		return !v
	}
	return false
}

func platformAgnosticDefaultConfig() *srvconfig.Config {
	return &srvconfig.Config{
		Version:          version.ConfigVersion,
		Root:             defaults.DefaultRootDir,
		State:            defaults.DefaultStateDir,
		DisabledPlugins:  []string{},
		RequiredPlugins:  []string{},
		StreamProcessors: streamProcessors(),
		Imports:          []string{defaults.DefaultConfigIncludePattern},
		Plugins: map[string]any{
			"io.containerd.server.v1.grpc": map[string]any{
				"address":               defaults.DefaultAddress,
				"max_recv_message_size": defaults.DefaultMaxRecvMsgSize,
				"max_send_message_size": defaults.DefaultMaxSendMsgSize,
			},
		},
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

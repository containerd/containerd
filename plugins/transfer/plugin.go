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

package transfer

import (
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/transfer/local"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"

	// Load packages with type registrations
	_ "github.com/containerd/containerd/pkg/transfer/archive"
	_ "github.com/containerd/containerd/pkg/transfer/image"
	_ "github.com/containerd/containerd/pkg/transfer/registry"
)

// Register local transfer service plugin
func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.TransferPlugin,
		ID:   "local",
		Requires: []plugin.Type{
			plugin.LeasePlugin,
			plugin.MetadataPlugin,
			plugin.DiffPlugin,
		},
		Config: defaultConfig(),
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*transferConfig)
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ms := m.(*metadata.DB)
			l, err := ic.Get(plugin.LeasePlugin)
			if err != nil {
				return nil, err
			}

			// Set configuration based on default or user input
			var lc local.TransferConfig
			lc.MaxConcurrentDownloads = config.MaxConcurrentDownloads
			lc.MaxConcurrentUploadedLayers = config.MaxConcurrentUploadedLayers
			for _, uc := range config.UnpackConfiguration {
				p, err := platforms.Parse(uc.Platform)
				if err != nil {
					return nil, fmt.Errorf("%s: platform configuration %v invalid", plugin.TransferPlugin, uc.Platform)
				}

				sn := ms.Snapshotter(uc.Snapshotter)
				if sn == nil {
					return nil, fmt.Errorf("snapshotter %q not found: %w", uc.Snapshotter, errdefs.ErrNotFound)
				}

				diffPlugins, err := ic.GetByType(plugin.DiffPlugin)
				if err != nil {
					return nil, fmt.Errorf("error loading diff plugins: %w", err)
				}
				var applier diff.Applier
				target := platforms.OnlyStrict(p)
				if uc.Differ != "" {
					plugin, ok := diffPlugins[uc.Differ]
					if !ok {
						return nil, fmt.Errorf("diff plugin %q: %w", uc.Differ, errdefs.ErrNotFound)
					}
					inst, err := plugin.Instance()
					if err != nil {
						return nil, fmt.Errorf("failed to get instance for diff plugin %q: %w", uc.Differ, err)
					}
					applier = inst.(diff.Applier)
				} else {
					for name, plugin := range diffPlugins {
						var matched bool
						for _, p := range plugin.Meta.Platforms {
							if target.Match(p) {
								matched = true
							}
						}
						if !matched {
							continue
						}
						if applier != nil {
							log.G(ic.Context).Warnf("multiple differs match for platform, set `differ` option to choose, skipping %q", name)
							continue
						}
						inst, err := plugin.Instance()
						if err != nil {
							return nil, fmt.Errorf("failed to get instance for diff plugin %q: %w", name, err)
						}
						applier = inst.(diff.Applier)
					}
				}
				if applier == nil {
					return nil, fmt.Errorf("no matching diff plugins: %w", errdefs.ErrNotFound)
				}

				up := unpack.Platform{
					Platform:       target,
					SnapshotterKey: uc.Snapshotter,
					Snapshotter:    sn,
					Applier:        applier,
				}
				lc.UnpackPlatforms = append(lc.UnpackPlatforms, up)
			}
			lc.RegistryConfigPath = config.RegistryConfigPath

			return local.NewTransferService(l.(leases.Manager), ms.ContentStore(), metadata.NewImageStore(ms), &lc), nil
		},
	})
}

type transferConfig struct {
	// MaxConcurrentDownloads is the max concurrent content downloads for pull.
	MaxConcurrentDownloads int `toml:"max_concurrent_downloads"`

	// MaxConcurrentUploadedLayers is the max concurrent uploads for push
	MaxConcurrentUploadedLayers int `toml:"max_concurrent_uploaded_layers"`

	// UnpackConfiguration is used to read config from toml
	UnpackConfiguration []unpackConfiguration `toml:"unpack_config"`

	// RegistryConfigPath is a path to the root directory containing registry-specific configurations
	RegistryConfigPath string `toml:"config_path"`
}

type unpackConfiguration struct {
	// Platform is the target unpack platform to match
	Platform string `toml:"platform"`

	// Snapshotter is the snapshotter to use to unpack
	Snapshotter string `toml:"snapshotter"`

	// Differ is the diff plugin to be used for apply
	Differ string `toml:"differ"`
}

func defaultConfig() *transferConfig {
	return &transferConfig{
		MaxConcurrentDownloads:      3,
		MaxConcurrentUploadedLayers: 3,
		UnpackConfiguration: []unpackConfiguration{
			{
				Platform:    platforms.Format(platforms.DefaultSpec()),
				Snapshotter: containerd.DefaultSnapshotter,
			},
		},
	}
}

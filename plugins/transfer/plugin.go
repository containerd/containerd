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

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/core/unpack"
	"github.com/containerd/containerd/v2/pkg/imageverifier"
	"github.com/containerd/containerd/v2/plugins"

	// Load packages with type registrations
	_ "github.com/containerd/containerd/v2/core/transfer/archive"
	_ "github.com/containerd/containerd/v2/core/transfer/image"
	_ "github.com/containerd/containerd/v2/core/transfer/registry"
)

// Register local transfer service plugin
func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TransferPlugin,
		ID:   "local",
		Requires: []plugin.Type{
			plugins.LeasePlugin,
			plugins.MetadataPlugin,
			plugins.DiffPlugin,
			plugins.ImageVerifierPlugin,
			plugins.SnapshotPlugin,
		},
		Config: defaultConfig(),
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*transferConfig)
			m, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ms := m.(*metadata.DB)

			var lc local.TransferConfig

			l, err := ic.GetSingle(plugins.LeasePlugin)
			if err != nil {
				return nil, err
			}
			lc.Leases = l.(leases.Manager)

			vps, err := ic.GetByType(plugins.ImageVerifierPlugin)
			if err != nil {
				return nil, err
			}
			if len(vps) > 0 {
				lc.Verifiers = make(map[string]imageverifier.ImageVerifier)
				for name, vp := range vps {
					lc.Verifiers[name] = vp.(imageverifier.ImageVerifier)
				}
			}

			// Set configuration based on default or user input
			lc.MaxConcurrentDownloads = config.MaxConcurrentDownloads
			lc.MaxConcurrentUploadedLayers = config.MaxConcurrentUploadedLayers

			// If UnpackConfiguration is not defined, set the default.
			// If UnpackConfiguration is defined and empty, ignore.
			if config.UnpackConfiguration == nil {
				config.UnpackConfiguration = defaultUnpackConfig()
			}
			for _, uc := range config.UnpackConfiguration {
				p, err := platforms.Parse(uc.Platform)
				if err != nil {
					return nil, fmt.Errorf("%s: platform configuration %v invalid", plugins.TransferPlugin, uc.Platform)
				}

				sn := ms.Snapshotter(uc.Snapshotter)
				if sn == nil {
					return nil, fmt.Errorf("snapshotter %q not found: %w", uc.Snapshotter, errdefs.ErrNotFound)
				}
				var snExports map[string]string
				if p := ic.Plugins().Get(plugins.SnapshotPlugin, uc.Snapshotter); p != nil {
					snExports = p.Meta.Exports
				}

				var applier diff.Applier
				target := platforms.Only(p)
				if uc.Differ != "" {
					inst, err := ic.GetByID(plugins.DiffPlugin, uc.Differ)
					if err != nil {
						return nil, fmt.Errorf("failed to get instance for diff plugin %q: %w", uc.Differ, err)
					}
					applier = inst.(diff.Applier)
				} else {
					for name, plugin := range ic.GetAll() {
						if plugin.Registration.Type != plugins.DiffPlugin {
							continue
						}
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
							log.G(ic.Context).Warnf("multiple differs match for platform, set `differ` option to choose, skipping %q", plugin.Registration.ID)
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

				// If CheckPlatformSupported is false, we will match all platforms
				if !config.CheckPlatformSupported {
					target = platforms.All
				}

				up := unpack.Platform{
					Platform:           target,
					SnapshotterKey:     uc.Snapshotter,
					Snapshotter:        sn,
					SnapshotterExports: snExports,
					Applier:            applier,
				}
				lc.UnpackPlatforms = append(lc.UnpackPlatforms, up)
			}
			lc.RegistryConfigPath = config.RegistryConfigPath

			return local.NewTransferService(ms.ContentStore(), metadata.NewImageStore(ms), lc), nil
		},
	})
}

type transferConfig struct {
	// MaxConcurrentDownloads is the max concurrent content downloads for pull.
	MaxConcurrentDownloads int `toml:"max_concurrent_downloads"`

	// MaxConcurrentUploadedLayers is the max concurrent uploads for push
	MaxConcurrentUploadedLayers int `toml:"max_concurrent_uploaded_layers"`

	// CheckPlatformSupported enables platform check specified in UnpackConfiguration
	CheckPlatformSupported bool `toml:"check_platform_supported"`

	// UnpackConfiguration is used to read config from toml
	UnpackConfiguration []unpackConfiguration `toml:"unpack_config,omitempty"`

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
		CheckPlatformSupported:      false,
	}
}

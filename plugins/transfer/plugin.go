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
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/transfer/local"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"

	// Load packages with type registrations
	_ "github.com/containerd/containerd/pkg/transfer/archive"
	_ "github.com/containerd/containerd/pkg/transfer/image"
)

// Register local transfer service plugin
func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.TransferPlugin,
		ID:   "local",
		Requires: []plugin.Type{
			plugin.LeasePlugin,
			plugin.MetadataPlugin,
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
			lc.MaxConcurrentDownloads = config.maxConcurrentDownloads
			lc.MaxConcurrentUploadedLayers = config.maxConcurrentUploadedLayers
			for _, uc := range config.unpackConfiguration {
				p, err := platforms.Parse(uc.platform)
				if err != nil {
					return nil, fmt.Errorf("%s: platform configuration %v invalid", plugin.TransferPlugin, uc.platform)
				}

				up := unpack.Platform{
					Platform:       platforms.OnlyStrict(p),
					SnapshotterKey: uc.snapshotter,
				}
				lc.UnpackPlatforms = append(lc.UnpackPlatforms, up)
			}
			lc.RegistryConfigPath = config.registryConfigPath

			return local.NewTransferService(l.(leases.Manager), ms.ContentStore(), metadata.NewImageStore(ms), &lc), nil
		},
	})
}

type transferConfig struct {
	// maxConcurrentDownloads is the max concurrent content downloads for pull.
	maxConcurrentDownloads int `toml:"max_concurrent_downloads"`

	// maxConcurrentUploadedLayers is the max concurrent uploads for push
	maxConcurrentUploadedLayers int `toml:"max_concurrent_uploaded_layers"`

	// unpackConfiguration is used to read config from toml
	unpackConfiguration []unpackConfiguration `toml:"unpack_config"`

	// registryConfigPath is a path to the root directory containing registry-specific configurations
	registryConfigPath string `toml:"config_path"`
}

type unpackConfiguration struct {
	platform    string
	snapshotter string
}

func defaultConfig() *transferConfig {
	return &transferConfig{
		maxConcurrentDownloads:      3,
		maxConcurrentUploadedLayers: 3,
		unpackConfiguration: []unpackConfiguration{
			{
				platform:    platforms.Format(platforms.DefaultSpec()),
				snapshotter: containerd.DefaultSnapshotter,
			},
		},
	}
}

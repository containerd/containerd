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

package plugin

import (
	"errors"

	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/snapshots/blockfile"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

// Config represents configuration for the native plugin.
type Config struct {
	// Root directory for the plugin
	RootPath string `toml:"root_path"`

	// ScratchFile is the scratch block file to use as an empty block
	ScratchFile string `toml:"scratch_file"`

	// FSType is the filesystem type for the mount
	FSType string `toml:"fs_type"`

	// MountOptions are options used for the mount
	MountOptions []string `toml:"mount_options"`

	// RecreateScratch always recreates the specified `ScratchFile`
	// on initialization of the plugin instead of using an existing.
	RecreateScratch bool `toml:"recreate_scratch"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "blockfile",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid blockfile configuration")
			}

			var opts []blockfile.Opt
			root := ic.Properties[plugins.PropertyRootDir]
			if len(config.RootPath) != 0 {
				root = config.RootPath
			}
			if config.ScratchFile != "" {
				opts = append(opts, blockfile.WithScratchFile(config.ScratchFile))
			}
			if config.FSType != "" {
				opts = append(opts, blockfile.WithFSType(config.FSType))
			}
			if len(config.MountOptions) > 0 {
				opts = append(opts, blockfile.WithMountOptions(config.MountOptions))
			}
			opts = append(opts, blockfile.WithRecreateScratch(config.RecreateScratch))

			return blockfile.NewSnapshotter(root, opts...)
		},
	})
}

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
	"fmt"

	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/snapshots/erofs"
	"github.com/docker/go-units"
)

const (
	capaRemapIDs     = "remap-ids"
	capaOnlyRemapIDs = "only-remap-ids"
)

// Config represents configuration for the native plugin.
type Config struct {
	// Root directory for the plugin
	RootPath string `toml:"root_path"`

	// MountOptions are options used for the EROFS overlayfs mount
	OvlOptions []string `toml:"ovl_mount_options"`

	// EnableFsverity enables fsverity for EROFS layers
	// Linux only
	EnableFsverity bool `toml:"enable_fsverity"`

	// If `SetImmutable` is enabled, IMMUTABLE_FL will be set on layer blobs.
	SetImmutable bool `toml:"set_immutable"`

	// DefaultSize is the default size of a writable layer in string
	DefaultSize string `toml:"default_size"`

	// MaxUnmergedLayers (>0) enables fsmerge when the number of image layers exceeds this value.
	MaxUnmergedLayers uint `toml:"max_unmerged_layers"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "erofs",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid erofs configuration")
			}

			var opts []erofs.Opt
			root := ic.Properties[plugins.PropertyRootDir]
			if len(config.RootPath) != 0 {
				root = config.RootPath
			}

			if len(config.OvlOptions) > 0 {
				opts = append(opts, erofs.WithOvlOptions(config.OvlOptions))
			}

			if config.EnableFsverity {
				opts = append(opts, erofs.WithFsverity())
			}

			if config.SetImmutable {
				opts = append(opts, erofs.WithImmutable())
			}

			if config.DefaultSize != "" {
				size, err := units.RAMInBytes(config.DefaultSize)
				if err != nil {
					return nil, fmt.Errorf("failed to parse default_size '%v': %w", config.DefaultSize, err)
				}
				opts = append(opts, erofs.WithDefaultSize(size))
			}

			if config.MaxUnmergedLayers > 0 {
				opts = append(opts, erofs.WithFsMergeThreshold(config.MaxUnmergedLayers))
			}

			// Don't bother supporting overlay's slow_chown, only RemapIDs
			ic.Meta.Capabilities = append(ic.Meta.Capabilities, capaOnlyRemapIDs)
			if ok, err := supportsIDMappedMounts(); err == nil && ok {
				opts = append(opts, erofs.WithRemapIDs())
				ic.Meta.Capabilities = append(ic.Meta.Capabilities, capaRemapIDs)
			}

			ic.Meta.Exports[plugins.SnapshotterRootDir] = root
			ic.Meta.Capabilities = append(ic.Meta.Capabilities, "rebase")
			return erofs.NewSnapshotter(root, opts...)
		},
	})
}

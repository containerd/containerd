//go:build linux

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

package overlay

import (
	"errors"

	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/snapshots/overlay"
	"github.com/containerd/containerd/v2/snapshots/overlay/overlayutils"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

const (
	capaRemapIds     = "remap-ids"
	capaOnlyRemapIds = "only-remap-ids"
)

// Config represents configuration for the overlay plugin.
type Config struct {
	// Root directory for the plugin
	RootPath      string `toml:"root_path"`
	UpperdirLabel bool   `toml:"upperdir_label"`
	SyncRemove    bool   `toml:"sync_remove"`

	// slowChown allows the plugin to fallback to a recursive chown if fast options (like
	// idmap mounts) are not available. See more info about the overhead this can have in
	// github.com/containerd/containerd/docs/user-namespaces/.
	SlowChown bool `toml:"slow_chown"`

	// MountOptions are options used for the overlay mount (not used on bind mounts)
	MountOptions []string `toml:"mount_options"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "overlayfs",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid overlay configuration")
			}

			root := ic.Properties[plugins.PropertyRootDir]
			if config.RootPath != "" {
				root = config.RootPath
			}

			var oOpts []overlay.Opt
			if config.UpperdirLabel {
				oOpts = append(oOpts, overlay.WithUpperdirLabel)
			}
			if !config.SyncRemove {
				oOpts = append(oOpts, overlay.AsynchronousRemove)
			}

			if len(config.MountOptions) > 0 {
				oOpts = append(oOpts, overlay.WithMountOptions(config.MountOptions))
			}
			if ok, err := overlayutils.SupportsIDMappedMounts(); err == nil && ok {
				oOpts = append(oOpts, overlay.WithRemapIds)
				ic.Meta.Capabilities = append(ic.Meta.Capabilities, capaRemapIds)
			}

			if config.SlowChown {
				oOpts = append(oOpts, overlay.WithSlowChown)
			} else {
				// If slowChown is false, we use capaOnlyRemapIds to signal we only
				// allow idmap mounts.
				ic.Meta.Capabilities = append(ic.Meta.Capabilities, capaOnlyRemapIds)
			}

			ic.Meta.Exports["root"] = root
			return overlay.NewSnapshotter(root, oOpts...)
		},
	})
}

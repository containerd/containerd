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
	"github.com/containerd/aufs"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/pkg/errors"
)

// Config represents configuration for the zfs plugin
type Config struct {
	// Root directory for the plugin
	RootPath string `toml:"root_path"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.SnapshotPlugin,
		ID:     "aufs",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			// get config
			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid aufs configuration")
			}

			// use default ic.Root as root path if config doesn't have a valid root path
			root := ic.Root
			if len(config.RootPath) != 0 {
				root = config.RootPath
			}
			ic.Meta.Exports["root"] = root

			snapshotter, err := aufs.New(root)
			if err != nil {
				return nil, errors.Wrap(plugin.ErrSkipPlugin, err.Error())
			}
			return snapshotter, nil
		},
	})
}

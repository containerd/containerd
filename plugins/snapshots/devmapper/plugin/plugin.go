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

package plugin

import (
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/snapshots/devmapper"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "devmapper",
		Config: &devmapper.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*devmapper.Config)
			if !ok {
				return nil, errors.New("invalid devmapper configuration")
			}

			if config.PoolName == "" {
				return nil, fmt.Errorf("devmapper not configured: %w", plugin.ErrSkipPlugin)
			}

			if config.RootPath == "" {
				config.RootPath = ic.Properties[plugins.PropertyRootDir]
			}

			ic.Meta.Exports[plugins.SnapshotterRootDir] = config.RootPath
			return devmapper.NewSnapshotter(ic.Context, config)
		},
	})
}

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

	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// Config represents configuration for the content plugin.
type Config struct {
	// Root directory for the plugin
	RootPath string `toml:"root_path"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.ContentPlugin,
		ID:     "content",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid content configuration")
			}

			root := ic.Properties[plugins.PropertyRootDir]
			if config.RootPath != "" {
				root = config.RootPath
			}

			ic.Meta.Exports[plugins.ContentRootDir] = root
			return local.NewStore(root)
		},
	})
}

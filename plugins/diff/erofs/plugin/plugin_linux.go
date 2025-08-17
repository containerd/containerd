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
	"fmt"

	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/diff/erofs"
)

// Config represents configuration for the erofs plugin.
type Config struct {
	// MkfsOptions are extra options used for the applier
	MkfsOptions []string `toml:"mkfs_options"`

	// EnableTarIndex enables the tar index mode where the index is generated
	// for tar content without extracting the tar
	EnableTarIndex bool `toml:"enable_tar_index"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.DiffPlugin,
		ID:   "erofs",
		Requires: []plugin.Type{
			plugins.MetadataPlugin,
		},
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			tarModeSupported, err := erofsutils.SupportGenerateFromTar()
			if err != nil {
				return nil, fmt.Errorf("failed to check mkfs.erofs availability: %v: %w", err, plugin.ErrSkipPlugin)
			}
			if !tarModeSupported {
				return nil, fmt.Errorf("mkfs.erofs does not support tar mode (--tar option), disabling erofs differ: %w", plugin.ErrSkipPlugin)
			}

			md, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())
			cs := md.(*metadata.DB).ContentStore()
			config := ic.Config.(*Config)

			var opts []erofs.DifferOpt

			if len(config.MkfsOptions) > 0 {
				opts = append(opts, erofs.WithMkfsOptions(config.MkfsOptions))
			}

			if config.EnableTarIndex {
				opts = append(opts, erofs.WithTarIndexMode())
			}

			return erofs.NewErofsDiffer(cs, opts...), nil
		},
	})
}

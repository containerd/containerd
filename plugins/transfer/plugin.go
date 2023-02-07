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
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/transfer/local"
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
		Config: local.DefaultConfig(),
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*local.TransferConfig)
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ms := m.(*metadata.DB)
			l, err := ic.Get(plugin.LeasePlugin)
			if err != nil {
				return nil, err
			}

			return local.NewTransferService(l.(leases.Manager), ms.ContentStore(), metadata.NewImageStore(ms), config), nil
		},
	})
}

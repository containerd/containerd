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
	"context"
	"fmt"

	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/transfer/custom"
	"github.com/containerd/containerd/plugin"
)

const customSnapshotterName = "custom"

// Register custom transfer plugin. Because this file is named "custom.go" and
// lives in the same package as "plugin.go", its init() runs first (alphabetical
// file-name ordering). The gRPC transfer service iterates TransferPlugin
// instances in order; our plugin returns ErrNotImplemented for non-pull
// operations so the stock "local" plugin handles those.
func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.TransferPlugin,
		ID:   "custom",
		Requires: []plugin.Type{
			plugin.LeasePlugin,
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ms := m.(*metadata.DB)

			l, err := ic.Get(plugin.LeasePlugin)
			if err != nil {
				return nil, err
			}

			sn := ms.Snapshotter(customSnapshotterName)
			if sn == nil {
				return nil, fmt.Errorf("custom transfer plugin: snapshotter %q not found", customSnapshotterName)
			}

			// TODO: Replace this placeholder with your real black-box fetch logic.
			fetchFn := func(ctx context.Context, imageRef string, targetDir string) error {
				log.G(ctx).Infof("custom fetch: imageRef=%s targetDir=%s", imageRef, targetDir)
				// Your implementation goes here.
				// targetDir is already a tmpfs mount with huge=advise and noatime.
				// Populate it with the rootfs content for imageRef.
				return fmt.Errorf("custom fetch not yet implemented for %s", imageRef)
			}

			return custom.NewTransferService(
				l.(leases.Manager),
				metadata.NewImageStore(ms),
				sn,
				customSnapshotterName,
				fetchFn,
			), nil
		},
	})
}

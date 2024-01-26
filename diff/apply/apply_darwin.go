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

package apply

import (
	"context"
	"io"
	"os"

	"github.com/containerd/containerd/v2/archive"
	"github.com/containerd/containerd/v2/mount"
)

func apply(ctx context.Context, mounts []mount.Mount, r io.Reader, _sync bool) error {
	// We currently do not support mounts nor bind mounts on MacOS in the containerd daemon.
	// Using this as an exception to enable native snapshotter and allow further research.
	if len(mounts) == 1 && mounts[0].Type == "bind" {
		opts := []archive.ApplyOpt{}

		if os.Getuid() != 0 {
			opts = append(opts, archive.WithNoSameOwner())
		}

		path := mounts[0].Source
		_, err := archive.Apply(ctx, path, r, opts...)
		return err

		// TODO: Do we need to sync all the filesystems?
	}

	return mount.WithTempMount(ctx, mounts, func(root string) error {
		_, err := archive.Apply(ctx, root, r)
		return err
	})
}

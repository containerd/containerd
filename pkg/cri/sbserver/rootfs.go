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

package sbserver

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/opencontainers/selinux/go-selinux/label"
)

func setupContainerRootfs(ctx context.Context, container containerd.Container, s snapshots.Snapshotter) ([]mount.Mount, error) {
	ci, err := container.Info(ctx)
	if err != nil {
		return nil, err
	}

	if ci.SnapshotKey != "" {
		if ci.Snapshotter == "" {
			return nil, fmt.Errorf("unable to resolve rootfs mounts without snapshotter on container: %w", errdefs.ErrInvalidArgument)
		}

		mounts, err := s.Mounts(ctx, ci.SnapshotKey)
		if err != nil {
			return nil, err
		}
		spec, err := container.Spec(ctx)
		if err != nil {
			return nil, err
		}
		for i, m := range mounts {
			if spec.Linux != nil && spec.Linux.MountLabel != "" {
				context := label.FormatMountLabel("", spec.Linux.MountLabel)
				if context != "" {
					m.Options = append(m.Options, context)
				}
			}

			// Add volatile option here, it should be the safest place doing this.
			if m.Type == "overlay" {
				mounts[i].Options = append(m.Options, "volatile")
			}
		}

		return mounts, nil
	}

	return nil, errdefs.ErrInvalidArgument
}

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

package opts

import (
	"context"
	"fmt"

	"github.com/containerd/errdefs"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/snapshots"
)

const persistImageDigestLabel = "persist.containerd.dev/image-digest"

// WithPersistentSnapshot reuses a named writable snapshot if it already exists,
// otherwise it creates it from the supplied image.
func WithPersistentSnapshot(id string, i containerd.Image, expectedImageRef string, labels map[string]string, appendSnapshotLabels bool, opts ...snapshots.Opt) containerd.NewContainerOpts {
	return func(ctx context.Context, client *containerd.Client, c *containers.Container) error {
		if c.Snapshotter == "" {
			return fmt.Errorf("no snapshotter set for persistent snapshot %q", id)
		}

		snapshotter := client.SnapshotService(c.Snapshotter)
		info, err := snapshotter.Stat(ctx, id)
		if err == nil {
			if expectedImageRef != "" {
				got := ""
				if info.Labels != nil {
					got = info.Labels[persistImageDigestLabel]
				}
				if got != expectedImageRef {
					return fmt.Errorf("persistent snapshot %q was created for image %q, not %q: %w", id, got, expectedImageRef, errdefs.ErrFailedPrecondition)
				}
			}
			if err := containerd.WithSnapshot(id)(ctx, client, c); err != nil {
				return err
			}
			c.Image = i.Name()
			return nil
		}
		if !errdefs.IsNotFound(err) {
			return err
		}

		snapshotOpts := make([]snapshots.Opt, 0, len(opts)+1)
		if len(labels) > 0 {
			snapshotOpts = append(snapshotOpts, snapshots.WithLabels(labels))
		}
		snapshotOpts = append(snapshotOpts, opts...)
		return WithNewSnapshot(id, i, appendSnapshotLabels, snapshotOpts...)(ctx, client, c)
	}
}

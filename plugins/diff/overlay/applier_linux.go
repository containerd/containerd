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
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moby/sys/userns"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/errdefs"
)

// Apply applies the diff from the reader to the provided mounts.
// The mounts must be a single overlay or bind mount.
// If sync is true, the filesystem will be synced after applying the diff.
func Apply(ctx context.Context, mounts []mount.Mount, r io.Reader, sync bool) (retErr error) {
	// OverlayConvertWhiteout (mknod c 0 0) doesn't work in userns.
	// https://github.com/containerd/containerd/issues/3762
	if userns.RunningInUserNS() {
		return errdefs.ErrNotImplemented
	}
	if len(mounts) != 1 {
		return errdefs.ErrNotImplemented
	}

	var (
		mntPath string
		parents []string
	)

	switch mounts[0].Type {
	case "overlay":
		mntPath, parents, retErr = getOverlayPath(mounts[0].Options)
	case "bind":
		mntPath = mounts[0].Source
		if len(mntPath) == 0 {
			retErr = fmt.Errorf("empty source for bind mount: %w", errdefs.ErrInvalidArgument)
		}
	default:
		retErr = fmt.Errorf("unsupported mount type %q: %w", mounts[0].Type, errdefs.ErrNotImplemented)
	}

	if retErr != nil {
		return
	}

	opts := []archive.ApplyOpt{
		archive.WithConvertWhiteout(archive.OverlayConvertWhiteout),
	}
	if len(parents) > 0 {
		opts = append(opts, archive.WithParents(parents))
	}

	_, retErr = archive.Apply(ctx, mntPath, r, opts...)

	if retErr == nil && sync {
		retErr = doSyncFs(mntPath)
	}

	return
}

func getOverlayPath(options []string) (upper string, lower []string, err error) {
	const upperdirPrefix = "upperdir="
	const lowerdirPrefix = "lowerdir="

	for _, o := range options {
		if after, ok := strings.CutPrefix(o, upperdirPrefix); ok {
			upper = after
		} else if after, ok := strings.CutPrefix(o, lowerdirPrefix); ok {
			lower = strings.Split(after, ":")
		}
	}
	if upper == "" {
		return "", nil, fmt.Errorf("upperdir not found: %w", errdefs.ErrInvalidArgument)
	}

	return
}

func doSyncFs(file string) error {
	fd, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", file, err)
	}
	defer fd.Close()

	err = unix.Syncfs(int(fd.Fd()))
	if err != nil {
		return fmt.Errorf("failed to syncfs for %s: %w", file, err)
	}
	return nil
}

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
	"fmt"
	"io"
	"strings"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/userns"
)

func apply(ctx context.Context, mounts []mount.Mount, r io.Reader) error {
	switch {
	case len(mounts) == 1 && mounts[0].Type == "overlay":
		// OverlayConvertWhiteout (mknod c 0 0) doesn't work in userns.
		// https://github.com/containerd/containerd/issues/3762
		if userns.RunningInUserNS() {
			break
		}
		path, parents, err := getOverlayPath(mounts[0].Options)
		if err != nil {
			if errdefs.IsInvalidArgument(err) {
				break
			}
			return err
		}
		opts := []archive.ApplyOpt{
			archive.WithConvertWhiteout(archive.OverlayConvertWhiteout),
		}
		if len(parents) > 0 {
			opts = append(opts, archive.WithParents(parents))
		}
		_, err = archive.Apply(ctx, path, r, opts...)
		return err
	case len(mounts) == 1 && mounts[0].Type == "aufs":
		path, parents, err := getAufsPath(mounts[0].Options)
		if err != nil {
			if errdefs.IsInvalidArgument(err) {
				break
			}
			return err
		}
		opts := []archive.ApplyOpt{
			archive.WithConvertWhiteout(archive.AufsConvertWhiteout),
		}
		if len(parents) > 0 {
			opts = append(opts, archive.WithParents(parents))
		}
		_, err = archive.Apply(ctx, path, r, opts...)
		return err
	}
	return mount.WithTempMount(ctx, mounts, func(root string) error {
		_, err := archive.Apply(ctx, root, r)
		return err
	})
}

func getOverlayPath(options []string) (upper string, lower []string, err error) {
	const upperdirPrefix = "upperdir="
	const lowerdirPrefix = "lowerdir="

	for _, o := range options {
		if strings.HasPrefix(o, upperdirPrefix) {
			upper = strings.TrimPrefix(o, upperdirPrefix)
		} else if strings.HasPrefix(o, lowerdirPrefix) {
			lower = strings.Split(strings.TrimPrefix(o, lowerdirPrefix), ":")
		}
	}
	if upper == "" {
		return "", nil, fmt.Errorf("upperdir not found: %w", errdefs.ErrInvalidArgument)
	}

	return
}

// getAufsPath handles options as given by the containerd aufs package only,
// formatted as "br:<upper>=rw[:<lower>=ro+wh]*"
func getAufsPath(options []string) (upper string, lower []string, err error) {
	const (
		sep      = ":"
		brPrefix = "br:"
		rwSuffix = "=rw"
		roSuffix = "=ro+wh"
	)
	for _, o := range options {
		if strings.HasPrefix(o, brPrefix) {
			o = strings.TrimPrefix(o, brPrefix)
		} else {
			continue
		}

		for _, b := range strings.Split(o, sep) {
			if strings.HasSuffix(b, rwSuffix) {
				if upper != "" {
					return "", nil, fmt.Errorf("multiple rw branch found: %w", errdefs.ErrInvalidArgument)
				}
				upper = strings.TrimSuffix(b, rwSuffix)
			} else if strings.HasSuffix(b, roSuffix) {
				if upper == "" {
					return "", nil, fmt.Errorf("rw branch be first: %w", errdefs.ErrInvalidArgument)
				}
				lower = append(lower, strings.TrimSuffix(b, roSuffix))
			} else {
				return "", nil, fmt.Errorf("unhandled aufs suffix: %w", errdefs.ErrInvalidArgument)
			}

		}
	}
	if upper == "" {
		return "", nil, fmt.Errorf("rw branch not found: %w", errdefs.ErrInvalidArgument)
	}
	return
}

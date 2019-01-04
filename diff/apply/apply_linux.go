// +build linux

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
	"strings"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/mount"
	"github.com/pkg/errors"
)

func apply(ctx context.Context, mounts []mount.Mount, r io.Reader) error {
	switch {
	case len(mounts) == 1 && mounts[0].Type == "overlay":
		path, err := getOverlayPath(mounts[0].Options)
		if err != nil {
			return err
		}
		_, err = archive.Apply(ctx, path, r,
			archive.WithConvertWhiteout(archive.OverlayConvertWhiteout))
		return err
	case len(mounts) == 1 && mounts[0].Type == "aufs":
		path, err := getAufsPath(mounts[0].Options)
		if err != nil {
			return err
		}
		_, err = archive.Apply(ctx, path, r,
			archive.WithConvertWhiteout(archive.AufsConvertWhiteout))
		return err
	default:
		return mount.WithTempMount(ctx, mounts, func(root string) error {
			_, err := archive.Apply(ctx, root, r)
			return err
		})
	}
}

func getOverlayPath(options []string) (string, error) {
	const upperdirPrefix = "upperdir="
	for _, o := range options {
		if strings.HasPrefix(o, upperdirPrefix) {
			return strings.TrimPrefix(o, upperdirPrefix), nil
		}
	}
	return "", errors.New("upperdir not found")
}

func getAufsPath(options []string) (string, error) {
	const (
		sep       = ":"
		brPrefix1 = "br:"
		brPrefix2 = "br="
		rwSuffix  = "=rw"
	)
	for _, o := range options {
		if strings.HasPrefix(o, brPrefix1) {
			o = strings.TrimPrefix(o, brPrefix1)
		} else if strings.HasPrefix(o, brPrefix2) {
			o = strings.TrimPrefix(o, brPrefix2)
		} else {
			continue
		}
		for _, b := range strings.Split(o, sep) {
			if strings.HasSuffix(b, rwSuffix) {
				return strings.TrimSuffix(b, rwSuffix), nil
			}
		}
		break
	}
	return "", errors.New("rw branch not found")
}

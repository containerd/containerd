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

package erofs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/fsview"
	"github.com/containerd/errdefs"
	"github.com/erofs/go-erofs"
)

func init() {
	fsview.Register(fsview.FSHandler{
		HandleMount: handleMount,
		Getxattr:    getxattr,
		IsWhiteout:  isWhiteout,
	})
}

func handleMount(m mount.Mount) (fsview.View, error) {
	if m.Type != "erofs" {
		return nil, errdefs.ErrNotImplemented
	}

	f, err := os.Open(m.Source)
	if err != nil {
		return nil, err
	}

	var extraDevices []io.ReaderAt
	var closers []io.Closer
	closers = append(closers, f)

	for _, opt := range m.Options {
		if devPath, ok := strings.CutPrefix(opt, "device="); ok {
			if devPath == "" {
				continue
			}
			df, err := os.Open(devPath)
			if err != nil {
				for _, c := range closers {
					c.Close()
				}
				return nil, err
			}
			closers = append(closers, df)
			extraDevices = append(extraDevices, df)
		}
	}

	var opts []erofs.OpenOpt
	if len(extraDevices) > 0 {
		opts = append(opts, erofs.WithExtraDevices(extraDevices...))
	}

	efs, err := erofs.Open(f, opts...)
	if err != nil {
		for _, c := range closers {
			c.Close()
		}
		return nil, err
	}

	return &erofsView{
		FS:      efs,
		closers: closers,
	}, nil
}

type erofsView struct {
	fs.FS
	closers []io.Closer
}

func (v *erofsView) Close() error {
	var errs []error
	for _, c := range v.closers {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

func getxattr(f fs.File, name string) (string, bool) {
	fi, err := f.Stat()
	if err != nil {
		return "", false
	}
	estatfi, ok := fi.Sys().(*erofs.Stat)
	if !ok {
		return "", false
	}
	val, ok := estatfi.Xattrs[name]
	return val, ok
}

func isWhiteout(fi fs.FileInfo) bool {
	if (fi.Mode() & fs.ModeCharDevice) == 0 {
		return false
	}
	estatfi, ok := fi.Sys().(*erofs.Stat)
	if !ok {
		return false
	}
	return estatfi.Rdev == 0
}

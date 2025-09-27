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

package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/mount"
)

// MkdirHandler returns a handler which ensures a directory
// is created with the correct permission and ownership
func MkdirHandler(roots ...string) (mount.Handler, error) {
	var rootMap = map[string]*os.Root{}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("mkdir handler root %q must be absolute path: %w", root, errdefs.ErrInvalidArgument)
		}
		r, err := os.OpenRoot(root)
		if err != nil {
			return nil, fmt.Errorf("failed to open root %q: %w", root, err)
		}
		rootMap[filepath.Clean(root)+string(filepath.Separator)] = r
	}

	return &mkdirHandler{
		rootMap: rootMap,
	}, nil
}

type mkdirHandler struct {
	rootMap map[string]*os.Root
}

func (h *mkdirHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	if m.Type != "mkdir" {
		return mount.ActiveMount{}, errdefs.ErrNotImplemented
	}

	var r *os.Root
	var subpath string

	for path, root := range h.rootMap {
		if strings.HasPrefix(m.Source, path) {
			r = root
			subpath = strings.TrimPrefix(m.Source, path)
			break
		}
	}
	if r == nil {
		return mount.ActiveMount{}, fmt.Errorf("no root %q configured for mkdir: %w", m.Source, errdefs.ErrNotImplemented)
	}

	// Find root map

	var (
		luid                 = os.Getuid()
		lgid                 = os.Getgid()
		uid, gid             = luid, lgid
		mode     os.FileMode = 0700
		err      error
	)
	// Parse options
	for _, o := range m.Options {
		key, value, ok := strings.Cut(o, "=")
		if !ok {
			key = o
			value = "true"
		}
		switch key {
		case "uid":
			uid, err = strconv.Atoi(value)
		case "gid":
			gid, err = strconv.Atoi(value)
		case "mode":
			var p uint64
			p, err = strconv.ParseUint(value, 8, 32)
			if err == nil {
				mode = os.FileMode(p)
				if mode != mode&os.ModePerm {
					err = fmt.Errorf("invalid mode %o", p)
				}
			}
		default:
			return mount.ActiveMount{}, fmt.Errorf("unknown mount option %s: %w", key, errdefs.ErrInvalidArgument)
		}
		if err != nil {
			return mount.ActiveMount{}, fmt.Errorf("bad option %s: %w", key, err)
		}
	}

	if st, err := r.Stat(subpath); err == nil {
		if st.Mode()&os.ModePerm != mode {
			// TODO: Chmod support added in go1.25
			return mount.ActiveMount{}, fmt.Errorf("chmod not supported yet for mkdir handler: %w", errdefs.ErrNotImplemented)
		}
		// TODO: check ownership, chown support added in go1.25
	} else if os.IsNotExist(err) {
		// TODO: MkdirAll added in go1.25
		if err := r.Mkdir(subpath, mode); err != nil {
			return mount.ActiveMount{}, fmt.Errorf("failed to create directory %q: %w", m.Source, err)
		}
		if luid != -1 && (luid != uid || lgid != gid) {
			// TODO: Chown support added in go1.25
			//if err := r.Chown(subpath, uid, gid); err != nil {
			//	return fmt.Errorf("failed to chown directory %q: %w", m.Source, err)
			//}
			return mount.ActiveMount{}, fmt.Errorf("chown not supported yet for mkdir handler: %w", errdefs.ErrNotImplemented)
		}
	} else {
		return mount.ActiveMount{}, fmt.Errorf("failed to stat %q: %w", m.Source, err)
	}

	t := time.Now()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &t,
		MountPoint: mp,
	}, nil
}

func (*mkdirHandler) Unmount(ctx context.Context, path string) error {
	return nil
}

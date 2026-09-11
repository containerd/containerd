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

package manager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/mount"
)

// mkdir is a mount transformer that creates directories
// it can be used to ensure directories are created before
// overlay options are applied or any case that might need
// a directory
type mkdir struct {
	rootMap map[string]*os.Root
}

// mkdirAction is one X-containerd.mkdir.* option, parsed but not yet
// applied to the filesystem.
type mkdirAction struct {
	root    *os.Root
	subpath string
	dir     string // original, unresolved-relative path, for error messages
	mode    os.FileMode
	uid     int
	gid     int
	luid    int
	lgid    int
}

// rewrite parses m's mkdir options into the mount value the kernel
// will see, and a deferredEnsure that creates the directories those
// options describe.
func (h *mkdir) rewrite(m mount.Mount) (mount.Mount, deferredEnsure, error) {
	var options []string
	var actions []mkdirAction
	for _, o := range m.Options {
		if mkdirOption, isMkdir := strings.CutPrefix(o, prefixMkdir); isMkdir {
			// Format is X-containerd.mkdir.path=value[:mode[:uid:gid]]

			value, isPath := strings.CutPrefix(mkdirOption, "path=")
			if !isPath {
				return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid mkdir option %q: %w", o, errdefs.ErrInvalidArgument)
			}
			parts := splitMkdirPathValue(value)
			var (
				dir      string
				mode     os.FileMode = 0700
				luid                 = os.Getuid()
				lgid                 = os.Getgid()
				uid, gid             = luid, lgid
				err      error
			)
			switch len(parts) {
			case 4:
				gid, err = strconv.Atoi(parts[3])
				if err != nil {
					return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid gid %q: %w", parts[3], errdefs.ErrInvalidArgument)
				}
				uid, err = strconv.Atoi(parts[2])
				if err != nil {
					return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid uid %q: %w", parts[2], errdefs.ErrInvalidArgument)
				}
				fallthrough
			case 2:
				var p uint64
				p, err = strconv.ParseUint(parts[1], 8, 32)
				if err == nil {
					mode = os.FileMode(p)
					if mode != mode&os.ModePerm {
						return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid mode %o", p)
					}
				} else {
					return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid mode %s: %w", parts[1], err)
				}
				fallthrough
			case 1:
				dir = parts[0]
			default:
				return mount.Mount{}, deferredEnsure{}, fmt.Errorf("invalid mkdir option %q: %w", o, errdefs.ErrInvalidArgument)
			}

			r, subpath, err := resolveRoot(h.rootMap, dir, "mkdir")
			if err != nil {
				return mount.Mount{}, deferredEnsure{}, err
			}

			actions = append(actions, mkdirAction{
				root:    r,
				subpath: subpath,
				dir:     dir,
				mode:    mode,
				uid:     uid,
				gid:     gid,
				luid:    luid,
				lgid:    lgid,
			})
		} else {
			options = append(options, o)
		}
	}
	m.Options = options

	targets := make([]string, len(actions))
	for i, a := range actions {
		target, err := filepath.Abs(filepath.Join(a.root.Name(), a.subpath))
		if err != nil {
			return mount.Mount{}, deferredEnsure{}, fmt.Errorf("failed to resolve absolute path for %q: %w", a.dir, err)
		}
		targets[i] = target
	}
	ensure := deferredEnsure{
		targets: targets,
		run: func(ctx context.Context) error {
			for _, a := range actions {
				if err := a.apply(); err != nil {
					return err
				}
			}
			return nil
		},
	}
	return m, ensure, nil
}

// apply creates a's directory if it does not already exist. An
// existing path is accepted only if it is already a directory with
// matching permissions.
func (a mkdirAction) apply() error {
	st, err := a.root.Stat(a.subpath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %q: %w", a.dir, err)
		}
		return a.create()
	}
	return a.validate(st)
}

// create makes a's directory, tolerating a concurrent boundary ensure
// for the same target having just created it first.
func (a mkdirAction) create() error {
	// TODO: MkdirAll added in go1.25
	if err := a.root.Mkdir(a.subpath, a.mode); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create directory %q: %w", a.dir, err)
		}
		st, err := a.root.Stat(a.subpath)
		if err != nil {
			return fmt.Errorf("failed to stat %q: %w", a.dir, err)
		}
		return a.validate(st)
	}
	if a.luid != -1 && (a.luid != a.uid || a.lgid != a.gid) {
		// TODO: Chown support added in go1.25
		//if err := a.root.Chown(a.subpath, a.uid, a.gid); err != nil {
		//	return fmt.Errorf("failed to chown directory %q: %w", a.dir, err)
		//}
		return fmt.Errorf("chown not supported yet for mkdir handler: %w", errdefs.ErrNotImplemented)
	}
	return nil
}

// validate reports whether st, describing something already at a's
// target, satisfies a's directory and mode requirements.
func (a mkdirAction) validate(st os.FileInfo) error {
	if !st.IsDir() {
		return fmt.Errorf("mkdir target %q exists and is not a directory: %w", a.dir, errdefs.ErrFailedPrecondition)
	}
	if st.Mode()&os.ModePerm != a.mode {
		// TODO: Chmod support added in go1.25
		return fmt.Errorf("chmod not supported yet for mkdir handler: %w", errdefs.ErrNotImplemented)
	}
	// TODO: check ownership, chown support added in go1.25
	return nil
}

// Transform implements mount.Transformer by running rewrite and its
// ensure together.
func (h *mkdir) Transform(ctx context.Context, m mount.Mount, _ []mount.ActiveMount) (mount.Mount, error) {
	rewritten, ensure, err := h.rewrite(m)
	if err != nil {
		return rewritten, err
	}
	if err := ensure.run(ctx); err != nil {
		return mount.Mount{}, err
	}
	return rewritten, nil
}

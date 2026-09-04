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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/docker/go-units"
)

type mkfs struct {
	rootMap map[string]*os.Root
}

// rewrite parses m's mkfs options into the mount value the kernel
// will actually see, and returns a closure which performs the file
// creation those options describe. rewrite itself touches no
// filesystem state: resolving a mount well enough to identify it,
// including deduplicating it against one which already exists, never
// has to wait on that side effect, only the returned closure does.
func (t *mkfs) rewrite(m mount.Mount) (mount.Mount, func(ctx context.Context) error, error) {
	r, subpath, err := resolveRoot(t.rootMap, m.Source, "mkfs")
	if err != nil {
		return m, nil, err
	}

	var (
		size int64
		id   string
		fs   = "ext4"
	)
	var options []string
	for _, o := range m.Options {
		if mkfsOption, isMkfs := strings.CutPrefix(o, prefixMkfs); isMkfs {
			key, value, ok := strings.Cut(mkfsOption, "=")
			if !ok {
				key = o
				value = "true"
			}
			switch key {
			case "size":
				var err error
				size, err = units.RAMInBytes(value)
				if err != nil {
					return mount.Mount{}, nil, fmt.Errorf("bad option %s: %w", key, err)
				}
			case "fs":
				fs = value
			case "uuid":
				id = value
			default:
				return mount.Mount{}, nil, fmt.Errorf("unknown mount option %s: %w", key, errdefs.ErrInvalidArgument)
			}

		} else {
			options = append(options, o)
		}
	}
	m.Options = options
	if size == 0 {
		return mount.Mount{}, nil, fmt.Errorf("mkfs requires mkfs.size option: %w", errdefs.ErrInvalidArgument)
	}

	source := m.Source
	ensure := func(ctx context.Context) error {
		return ensureMkfsImage(ctx, r, subpath, source, size, fs, id)
	}
	return m, ensure, nil
}

// Transform implements mount.Transformer by running rewrite and its
// ensure closure together, one after the other, so a caller which
// still wants the historical parse-then-act-in-one-call behavior gets
// exactly that.
func (t *mkfs) Transform(ctx context.Context, m mount.Mount, _ []mount.ActiveMount) (mount.Mount, error) {
	log.G(ctx).Debugf("transforming mkfs mount: %+v", m)
	rewritten, ensure, err := t.rewrite(m)
	if err != nil {
		log.G(ctx).WithError(err).Debugf("skipping mkfs")
		return rewritten, err
	}
	if err := ensure(ctx); err != nil {
		return mount.Mount{}, err
	}
	return rewritten, nil
}

// ensureMkfsImage creates and formats the backing file described by
// subpath if it does not already exist. An existing regular file is
// assumed to already be formatted correctly: this is only ever called
// to bring reality into line with a mount this package's own records
// describe, never on a path outside its control. An existing path
// which is not a regular file at all is reported rather than accepted
// as one: whatever depends on subpath actually being the backing file
// would otherwise fail later, mounting a directory or device instead,
// with an error that no longer points back to this being why.
func ensureMkfsImage(ctx context.Context, r *os.Root, subpath, source string, size int64, fs, id string) error {
	if st, err := r.Stat(subpath); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("mkfs backing file %q exists and is not a regular file: %w", source, errdefs.ErrFailedPrecondition)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %q: %w", source, err)
	}

	createArgs := []string{"-q"}

	// TODO: Pre-resolve the binaries to absolute path on startup for supported fs types
	var binary string

	// Check fs
	switch fs {
	case "ext2", "ext3", "ext4":
		binary = fmt.Sprintf("mkfs.%s", fs)
		if id != "" {
			createArgs = append(createArgs, []string{"-U", id}...)
		}
	case "xfs":
		binary = "mkfs.xfs"
		if id != "" {
			createArgs = append(createArgs, []string{"-m", fmt.Sprintf("uuid=%s", id)}...)
		}
	default:
		return fmt.Errorf("unsupported filesystem %q: %w", fs, errdefs.ErrInvalidArgument)
	}

	f, err := r.OpenFile(subpath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return fmt.Errorf("failed to create file %q: %w", source, err)
	}

	createArgs = append(createArgs, f.Name())

	err = f.Truncate(size)
	f.Close()
	if err != nil {
		return fmt.Errorf("failed to truncate file %q: %w", source, err)
	}

	if err := createWritableImage(ctx, binary, createArgs...); err != nil {
		return fmt.Errorf("failed format %q: %w", source, err)
	}

	return nil
}

func createWritableImage(ctx context.Context, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %s: %w", filepath.Base(binary), out, err)
	}
	return nil
}

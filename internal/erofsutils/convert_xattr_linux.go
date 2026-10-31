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

package erofsutils

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	goerofs "github.com/erofs/go-erofs"
	"golang.org/x/sys/unix"
)

// applyXattrs walks srcDir and copies all extended attributes from every
// file/directory/symlink into the EROFS writer w.
//
// os.DirFS does not surface xattrs through the fs.FileInfo.Sys() interface,
// so CopyFrom silently drops them. This function performs a second pass over
// the same directory tree using raw syscalls to pick up any xattrs that were
// missed — most importantly the overlay opaque marker
// (trusted.overlay.opaque = "y") that overlayfs sets on directories that
// replace a lower-layer directory.
func applyXattrs(w *goerofs.Writer, srcDir string) error {
	return filepath.WalkDir(srcDir, func(osPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Build the absolute path inside the EROFS image.
		rel, err := filepath.Rel(srcDir, osPath)
		if err != nil {
			return fmt.Errorf("applyXattrs: rel %s: %w", osPath, err)
		}
		erofsPath := "/" + filepath.ToSlash(rel)
		if rel == "." {
			erofsPath = "/"
		}

		// List xattr names without following symlinks.
		names, err := listXattrs(osPath)
		if err != nil || len(names) == 0 {
			return nil //nolint:nilerr // missing xattr support is not an error
		}

		for _, name := range names {
			val, ok, err := getXattr(osPath, name)
			if err != nil {
				return fmt.Errorf("applyXattrs: getxattr %s %s: %w", erofsPath, name, err)
			}
			if !ok {
				// Attribute disappeared between list and get — benign race.
				continue
			}
			if err := w.Setxattr(erofsPath, name, string(val)); err != nil {
				return fmt.Errorf("applyXattrs: setxattr %s %s: %w", erofsPath, name, err)
			}
		}
		return nil
	})
}

// listXattrs returns the xattr names for path (without following symlinks).
// Returns nil, nil when xattrs are not supported or the list is empty.
func listXattrs(path string) ([]string, error) {
	// First call with nil dest to learn the required buffer size.
	sz, err := unix.Llistxattr(path, nil)
	if err != nil || sz == 0 {
		return nil, nil //nolint:nilerr
	}

	buf := make([]byte, sz)
	sz, err = unix.Llistxattr(path, buf)
	if err != nil {
		return nil, nil //nolint:nilerr
	}

	// The kernel returns a NUL-separated list of names.
	var names []string
	for name := range strings.SplitSeq(string(buf[:sz]), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// getXattr returns the value of the named xattr on path (without following
// symlinks). The second return value is false only when the attribute does not
// exist (e.g. it was removed between the Llistxattr and Lgetxattr calls).
// An empty value ("") is legitimate and is returned as ([]byte{}, true, nil).
func getXattr(path, name string) ([]byte, bool, error) {
	// First call with nil dest to learn the required buffer size.
	// sz == 0 means the attribute exists but has an empty value.
	sz, err := unix.Lgetxattr(path, name, nil)
	if err != nil {
		if err == unix.ENODATA {
			return nil, false, nil
		}
		return nil, false, err
	}
	if sz == 0 {
		return []byte{}, true, nil
	}

	buf := make([]byte, sz)
	sz, err = unix.Lgetxattr(path, name, buf)
	if err != nil {
		if err == unix.ENODATA {
			return nil, false, nil
		}
		return nil, false, err
	}
	return buf[:sz], true, nil
}

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

package fs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/log"
)

// XAttrErrorHandler transform a non-nil xattr error.
// Return nil to ignore an error.
// xattrKey can be empty for listxattr operation.
type XAttrErrorHandler func(dst, src, xattrKey string, err error) error

type copyDirOpts struct {
	xeh XAttrErrorHandler
	// xex contains a set of xattrs to exclude when copying
	xex      map[string]struct{}
	fileSync bool
	dirSync  bool
}

type CopyDirOpt func(*copyDirOpts) error

// WithXAttrErrorHandler allows specifying XAttrErrorHandler
// If nil XAttrErrorHandler is specified (default), CopyDir stops
// on a non-nil xattr error.
func WithXAttrErrorHandler(xeh XAttrErrorHandler) CopyDirOpt {
	return func(o *copyDirOpts) error {
		o.xeh = xeh
		return nil
	}
}

// WithAllowXAttrErrors allows ignoring xattr errors.
func WithAllowXAttrErrors() CopyDirOpt {
	xeh := func(dst, src, xattrKey string, err error) error {
		return nil
	}
	return WithXAttrErrorHandler(xeh)
}

// WithXAttrExclude allows for exclusion of specified xattr during CopyDir operation.
func WithXAttrExclude(keys ...string) CopyDirOpt {
	return func(o *copyDirOpts) error {
		if o.xex == nil {
			o.xex = make(map[string]struct{}, len(keys))
		}
		for _, key := range keys {
			o.xex[key] = struct{}{}
		}
		return nil
	}
}

// WithCopyFileSync ensures each file copied within CopyDir is fsynced to
// persistent storage before proceeding to the next file.
//
// By default, files are not synced. Versions prior to v0.5.0 never synced;
// v0.5.0 added unconditional per-file fsync which caused a performance
// regression.
func WithCopyFileSync() CopyDirOpt {
	return func(o *copyDirOpts) error {
		o.fileSync = true
		return nil
	}
}

// WithCopyDirSync ensures full crash durability during CopyDir.
// This implies file-level fsync (WithCopyFileSync) and additionally fsyncs
// each directory after all its entries have been copied.
//
// By default, neither files nor directories are synced. Versions prior to
// v0.5.0 never synced; v0.5.0 added unconditional per-file fsync which
// caused a performance regression.
func WithCopyDirSync() CopyDirOpt {
	return func(o *copyDirOpts) error {
		o.fileSync = true
		o.dirSync = true
		return nil
	}
}

// CopyDir copies the directory from src to dst.
// Most efficient copy of files is attempted.
func CopyDir(dst, src string, opts ...CopyDirOpt) error {
	var o copyDirOpts
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}
	inodes := map[uint64]string{}
	return copyDirectory(dst, src, inodes, &o)
}

func copyDirectory(dst, src string, inodes map[uint64]string, o *copyDirOpts) error {
	stat, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", src, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("source %s is not directory", src)
	}

	if st, err := os.Stat(dst); err != nil {
		if err := os.Mkdir(dst, stat.Mode()); err != nil {
			return fmt.Errorf("failed to mkdir %s: %w", dst, err)
		}
	} else if !st.IsDir() {
		return fmt.Errorf("cannot copy to non-directory: %s", dst)
	} else {
		if err := os.Chmod(dst, stat.Mode()); err != nil {
			return fmt.Errorf("failed to chmod on %s: %w", dst, err)
		}
	}

	if err := copyFileInfo(stat, src, dst); err != nil {
		return fmt.Errorf("failed to copy file info for %s: %w", dst, err)
	}

	if err := copyXAttrs(dst, src, o.xex, o.xeh); err != nil {
		return fmt.Errorf("failed to copy xattrs: %w", err)
	}

	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	dr := &dirReader{f: f}

	handleEntry := func(entry os.DirEntry) error {
		source := filepath.Join(src, entry.Name())
		target := filepath.Join(dst, entry.Name())

		fileInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("failed to get file info for %s: %w", entry.Name(), err)
		}

		switch {
		case entry.IsDir():
			if err := copyDirectory(target, source, inodes, o); err != nil {
				return err
			}
			return nil
		case (fileInfo.Mode() & os.ModeType) == 0:
			link, err := getLinkSource(target, fileInfo, inodes)
			if err != nil {
				return fmt.Errorf("failed to get hardlink: %w", err)
			}
			if link != "" {
				if err := os.Link(link, target); err != nil {
					return fmt.Errorf("failed to create hard link: %w", err)
				}
			} else if err := copyFileWithOpts(target, source, o); err != nil {
				return fmt.Errorf("failed to copy files: %w", err)
			}
		case (fileInfo.Mode() & os.ModeSymlink) == os.ModeSymlink:
			link, err := os.Readlink(source)
			if err != nil {
				return fmt.Errorf("failed to read link: %s: %w", source, err)
			}
			if err := os.Symlink(link, target); err != nil {
				return fmt.Errorf("failed to create symlink: %s: %w", target, err)
			}
		case (fileInfo.Mode() & os.ModeDevice) == os.ModeDevice,
			(fileInfo.Mode() & os.ModeNamedPipe) == os.ModeNamedPipe,
			(fileInfo.Mode() & os.ModeSocket) == os.ModeSocket:
			if err := copyIrregular(target, fileInfo); err != nil {
				return fmt.Errorf("failed to create irregular file: %w", err)
			}
		default:
			log.L.Warnf("unsupported mode: %s: %s", source, fileInfo.Mode())
			return nil
		}

		if err := copyFileInfo(fileInfo, source, target); err != nil {
			return fmt.Errorf("failed to copy file info: %w", err)
		}

		if err := copyXAttrs(target, source, o.xex, o.xeh); err != nil {
			return fmt.Errorf("failed to copy xattrs: %w", err)
		}
		return nil
	}

	for {
		entry := dr.Next()
		if entry == nil {
			break
		}

		if err := handleEntry(entry); err != nil {
			return err
		}
	}
	if err := dr.Err(); err != nil {
		return err
	}

	if o.dirSync {
		if err := syncDirectory(dst); err != nil {
			return err
		}
	}

	return nil
}

func copyFileWithOpts(target, source string, o *copyDirOpts) error {
	if o.fileSync {
		return CopyFile(target, source, WithFileSync())
	}
	return CopyFile(target, source)
}

type CopyFileOpt func(*copyFileConfig)

type copyFileConfig struct {
	sync bool
}

// WithFileSync ensures the copied file is fsynced to persistent
// storage before returning.
//
// By default, files are not synced. Versions prior to v0.5.0 never synced;
// v0.5.0 added unconditional per-file fsync which caused a performance regression.
func WithFileSync() CopyFileOpt {
	return func(c *copyFileConfig) {
		c.sync = true
	}
}

// CopyFile copies the source file to the target.
// The most efficient means of copying is used for the platform.
func CopyFile(target, source string, opts ...CopyFileOpt) error {
	var cfg copyFileConfig
	for _, o := range opts {
		o(&cfg)
	}
	return copyFile(target, source, cfg.sync)
}

func openAndCopyFile(target, source string, sync bool) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", source, err)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to open target %s: %w", target, err)
	}
	defer tgt.Close()

	if _, err = io.Copy(tgt, src); err != nil {
		return err
	}
	if sync {
		if err := tgt.Sync(); err != nil {
			return fmt.Errorf("failed to sync target %s: %w", target, err)
		}
	}
	return nil
}

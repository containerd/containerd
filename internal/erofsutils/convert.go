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

// Package erofsutils provides pure-Go implementations of EROFS image creation.
//
// All functions use only:
//   - github.com/erofs/go-erofs — in-process EROFS writer
//   - github.com/containerd/continuity/tarconv — tar→EROFS conversion
//
// No external process is spawned. The implementations are cross-platform
// (Linux, macOS, Windows).
//
// # Running benchmarks
//
//	go test ./internal/erofsutils/... -bench=. -benchtime=3x -v
package erofsutils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/containerd/continuity/tarconv"
	"github.com/containerd/log"
	goerofs "github.com/erofs/go-erofs"
)

// ConvertTarErofs converts a tar stream r into a full EROFS image at
// layerPath using the pure-Go go-erofs + continuity/tarconv stack.
//
// OCI/AUFS whiteout entries (.wh.*) are translated to overlayfs
// char-device+xattr representation.
//
// The uuid parameter is accepted for API compatibility but is currently
// unused: go-erofs derives a deterministic superblock build-time from the
// image content. It will be plumbed through to [goerofs.WithBuildTime] once
// that API stabilises.
func ConvertTarErofs(ctx context.Context, r io.Reader, layerPath, uuid string) error {
	f, err := os.Create(layerPath)
	if err != nil {
		return fmt.Errorf("ConvertTarErofs: create output: %w", err)
	}
	defer f.Close()

	w := goerofs.Create(f)
	if err := tarconv.Apply(w, r); err != nil {
		return fmt.Errorf("ConvertTarErofs: apply tar: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("ConvertTarErofs: finalise EROFS: %w", err)
	}
	log.G(ctx).Debugf("ConvertTarErofs: wrote %s", layerPath)
	return nil
}

// DevicePath returns the path of the n-th data device of a layer whose image
// is stored in dir. A data device is a raw byte source the layer image
// addresses through EROFS multi-device chunk indexes; it is not an EROFS image
// itself. Devices are numbered from 0 in the order they are passed to the
// kernel as "device=" mount options.
func DevicePath(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("device-%d.blob", n))
}

// LayerDevices returns the data devices of the layer image stored in dir, in
// device order. A layer without data devices returns nil. Only regular files
// count; the lookup does not follow links.
func LayerDevices(dir string) ([]string, error) {
	var devices []string
	for n := 0; ; n++ {
		p := DevicePath(dir, n)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return devices, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to stat data device %q: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("data device %q is not a regular file", p)
		}
		devices = append(devices, p)
	}
}

// ConvertTarErofsIndex converts a tar stream r into an EROFS metadata image at
// layerPath whose file data lives in a separate data device at devicePath.
//
// The metadata image holds the superblock, inodes, and a chunk index for each
// regular file; it uses 512-byte blocks and declares one extra device. The data
// device holds the file contents, each starting on a 512-byte boundary. The
// image is mounted with the data device attached as "device=<devicePath>".
//
// Both files are written to temporary names in their target directories and
// renamed into place on success, so a failed conversion leaves neither behind.
//
// The uuid parameter is accepted for API compatibility but is currently unused.
func ConvertTarErofsIndex(ctx context.Context, r io.Reader, layerPath, devicePath, uuid string) (retErr error) {
	data, err := os.CreateTemp(filepath.Dir(devicePath), ".erofs-device-*")
	if err != nil {
		return fmt.Errorf("ConvertTarErofsIndex: create data device: %w", err)
	}
	meta, err := os.CreateTemp(filepath.Dir(layerPath), ".erofs-meta-*")
	if err != nil {
		data.Close()
		os.Remove(data.Name())
		return fmt.Errorf("ConvertTarErofsIndex: create metadata image: %w", err)
	}
	defer func() {
		data.Close()
		meta.Close()
		if retErr != nil {
			os.Remove(data.Name())
			os.Remove(meta.Name())
		}
	}()

	w := goerofs.Create(meta,
		goerofs.WithBlockSize(512),
		goerofs.WithDataFile(data),
	)
	if err := tarconv.Apply(w, r); err != nil {
		return fmt.Errorf("ConvertTarErofsIndex: apply tar: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("ConvertTarErofsIndex: finalise EROFS: %w", err)
	}
	// A layer without file data leaves the data device empty, but the image
	// still declares it. A zero-length device cannot be attached through a
	// loop device or formatted with dm-verity, so it gets one zero block; the
	// image addresses none of it.
	if fi, err := data.Stat(); err != nil {
		return fmt.Errorf("ConvertTarErofsIndex: stat data device: %w", err)
	} else if fi.Size() == 0 {
		if err := data.Truncate(512); err != nil {
			return fmt.Errorf("ConvertTarErofsIndex: pad data device: %w", err)
		}
	}
	// Both files are closed before they are renamed: Windows does not rename
	// an open file.
	for _, f := range []*os.File{data, meta} {
		if err := f.Sync(); err != nil {
			return fmt.Errorf("ConvertTarErofsIndex: sync %s: %w", f.Name(), err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("ConvertTarErofsIndex: close %s: %w", f.Name(), err)
		}
	}
	// The device goes into place first: a metadata image at layerPath is what
	// marks the layer as applied, and it must not be visible without its data.
	if err := os.Rename(data.Name(), devicePath); err != nil {
		return fmt.Errorf("ConvertTarErofsIndex: rename data device: %w", err)
	}
	if err := os.Rename(meta.Name(), layerPath); err != nil {
		os.Remove(devicePath)
		return fmt.Errorf("ConvertTarErofsIndex: rename metadata image: %w", err)
	}
	log.G(ctx).Debugf("ConvertTarErofsIndex: wrote %s with data device %s", layerPath, devicePath)
	return nil
}

// ConvertErofs converts a source directory srcDir into an EROFS image at
// layerPath using the pure-Go go-erofs writer.
//
// On platforms where go-erofs cannot extract Unix metadata from os.Stat()
// results (i.e., non-Linux, non-Darwin), file ownership and device numbers
// will be zero-valued. Permission bits and timestamps are always preserved.
//
// Extended attributes (xattrs) — including the overlayfs opaque marker
// trusted.overlay.opaque — are copied via a second pass using raw syscalls
// on Linux. This is necessary because os.DirFS does not expose xattrs through
// the fs.FileInfo.Sys() interface, so CopyFrom alone would silently drop them.
func ConvertErofs(ctx context.Context, layerPath, srcDir string) error {
	f, err := os.Create(layerPath)
	if err != nil {
		return fmt.Errorf("ConvertErofs: create output: %w", err)
	}
	defer f.Close()

	w := goerofs.Create(f)
	src := lazyDirFS{os.DirFS(srcDir).(dirFS)}
	if err := w.CopyFrom(src); err != nil {
		return fmt.Errorf("ConvertErofs: copy from dir: %w", err)
	}
	// Second pass: copy xattrs that os.DirFS does not surface through Sys().
	if err := applyXattrs(w, srcDir); err != nil {
		return fmt.Errorf("ConvertErofs: apply xattrs: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("ConvertErofs: finalise EROFS: %w", err)
	}
	log.G(ctx).Debugf("ConvertErofs: wrote %s from %s", layerPath, srcDir)
	return nil
}

// dirFS is the set of interfaces os.DirFS implements that Writer.CopyFrom
// uses to walk a directory.
type dirFS interface {
	fs.StatFS
	fs.ReadDirFS
	fs.ReadLinkFS
}

// lazyDirFS is a directory filesystem whose regular files are opened on their
// first read. Writer.CopyFrom opens every regular file while it walks the tree
// but reads the data only when the image is written, closing each file after
// reading it. With the files opened eagerly, a tree holding more files than
// the process may have open descriptors fails to convert.
type lazyDirFS struct {
	dirFS
}

func (fsys lazyDirFS) Open(name string) (fs.File, error) {
	fi, err := fsys.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return fsys.dirFS.Open(name)
	}
	return &lazyFile{fsys: fsys.dirFS, name: name, info: fi}, nil
}

// lazyFile opens the underlying file on its first read.
type lazyFile struct {
	fsys fs.FS
	name string
	info fs.FileInfo
	f    fs.File
}

func (l *lazyFile) Stat() (fs.FileInfo, error) { return l.info, nil }

func (l *lazyFile) Read(p []byte) (int, error) {
	if l.f == nil {
		f, err := l.fsys.Open(l.name)
		if err != nil {
			return 0, err
		}
		l.f = f
	}
	return l.f.Read(p)
}

func (l *lazyFile) Close() error {
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

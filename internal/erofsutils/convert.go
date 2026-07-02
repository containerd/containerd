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
	"fmt"
	"io"
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

// GenerateTarIndexAndAppendTar produces the EROFS tar-index format.
//
// It is a convenience wrapper around [GenerateTarIndexAndAppendTarTo] that
// derives the metadata output path as "fsmeta.erofs" in the same directory as
// layerPath.  The snapshot layout convention (fsmeta.erofs / layer.erofs in
// the same snapshot directory) is owned by the caller; this wrapper exists
// only to preserve the existing call-site signature.
//
// See [GenerateTarIndexAndAppendTarTo] for the full output description.
//
// The uuid parameter is accepted for API compatibility but is currently unused.
func GenerateTarIndexAndAppendTar(ctx context.Context, r io.Reader, layerPath, uuid string) error {
	metaPath := filepath.Join(filepath.Dir(layerPath), "fsmeta.erofs")
	return GenerateTarIndexAndAppendTarTo(ctx, r, metaPath, layerPath, uuid)
}

// GenerateTarIndexAndAppendTarTo produces the EROFS tar-index format, writing
// output to caller-specified paths.
//
// # Output layout (two files)
//
//   - metaPath: EROFS metadata image — superblock + inodes + chunk-index table.
//     Chunk indexes use DeviceID=1, which the EROFS kernel driver resolves via
//     the "device=" mount option pointing at dataPath.
//
//   - dataPath: Raw file payload bytes — one 512-byte-block-aligned region per
//     file.  No EROFS wrapper.  This file is supplied as device 1 when mounting
//     metaPath with the kernel EROFS driver.
//
// The caller decides where the two files live; the package imposes no naming
// convention.  The EROFS snapshotter uses "fsmeta.erofs" and "layer.erofs" in
// the same snapshot directory, but that is a snapshotter concern, not this
// package's.
//
// The uuid parameter is accepted for API compatibility but is currently unused.
func GenerateTarIndexAndAppendTarTo(ctx context.Context, r io.Reader, metaPath, dataPath, uuid string) error {
	// dataTemp receives raw file payload bytes written by go-erofs at
	// 512-byte-aligned positions. go-erofs records chunk indexes
	// (DeviceID=1) pointing into this file. It is renamed to dataPath on
	// success; we write to a temp file first so a partial write never leaves
	// a corrupt dataPath in place.
	dataTemp, err := os.CreateTemp(filepath.Dir(dataPath), ".erofs-tar-idx-data-*")
	if err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: create data temp: %w", err)
	}
	dataTempName := dataTemp.Name()
	dataDone := false
	defer func() {
		dataTemp.Close()
		if !dataDone {
			os.Remove(dataTempName)
		}
	}()

	// metaTemp receives the EROFS metadata-only image (superblock + inodes +
	// chunk table). No payload bytes are written here; data lives in dataTemp.
	// It is renamed to metaPath on success.
	metaTemp, err := os.CreateTemp(filepath.Dir(metaPath), ".erofs-tar-idx-meta-*.erofs")
	if err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: create meta temp: %w", err)
	}
	metaTempName := metaTemp.Name()
	metaDone := false
	defer func() {
		metaTemp.Close()
		if !metaDone {
			os.Remove(metaTempName)
		}
	}()

	// Block size 512 matches tar's natural granularity: file data in a tar
	// stream always starts on a 512-byte boundary (one tar header block).
	// Chunk indexes in the metadata image reference blocks in dataTemp
	// starting at offset 0, which will match the start of dataPath after
	// the rename below.
	w := goerofs.Create(metaTemp,
		goerofs.WithBlockSize(512),
		goerofs.WithDataFile(dataTemp),
	)

	if err := tarconv.Apply(w, r); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: apply tar: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: finalise EROFS: %w", err)
	}

	// Flush and rename both temp files to their final paths.
	// metaTemp → metaPath  (EROFS metadata image, mounted as primary)
	// dataTemp → dataPath  (raw payload data, mounted as device 1)
	if err := metaTemp.Sync(); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: sync meta: %w", err)
	}
	metaTemp.Close()
	if err := os.Rename(metaTempName, metaPath); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: rename meta: %w", err)
	}
	metaDone = true

	if err := dataTemp.Sync(); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: sync data: %w", err)
	}
	dataTemp.Close()
	if err := os.Rename(dataTempName, dataPath); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTarTo: rename data: %w", err)
	}
	dataDone = true

	log.G(ctx).Debugf("GenerateTarIndexAndAppendTarTo: wrote EROFS metadata to %s, data to %s", metaPath, dataPath)
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
	src := os.DirFS(srcDir)
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

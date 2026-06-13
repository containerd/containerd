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
// # Output layout
//
//	[EROFS metadata image — superblock + inodes + chunk-index table]
//	[File payload bytes — one block-padded 512-byte region per file]
//
// The EROFS metadata portion contains only filesystem metadata and
// chunk-index entries (DeviceID=1). Each file's payload is stored at a
// 512-byte-block-aligned position in the appended data region; the chunk
// indexes record those block offsets so that a consumer using the EROFS
// kernel driver can read file content directly from the combined blob
// without unpacking.
//
// The uuid parameter is accepted for API compatibility but is currently unused.
func GenerateTarIndexAndAppendTar(ctx context.Context, r io.Reader, layerPath, uuid string) error {
	// dataFile receives raw file payload bytes written by go-erofs at
	// 512-byte-aligned positions. go-erofs records chunk indexes
	// (DeviceID=1) pointing into this file.
	dataFile, err := os.CreateTemp("", "erofs-tar-idx-data-*")
	if err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: create data temp: %w", err)
	}
	defer os.Remove(dataFile.Name())
	defer dataFile.Close()

	// metaFile receives the EROFS metadata image (superblock + inodes +
	// chunk table). No payload bytes are written here.
	metaFile, err := os.CreateTemp("", "erofs-tar-idx-meta-*.erofs")
	if err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: create meta temp: %w", err)
	}
	defer os.Remove(metaFile.Name())
	defer metaFile.Close()

	// Block size 512 matches tar's natural granularity: file data in a tar
	// stream always starts on a 512-byte boundary (one tar header block).
	w := goerofs.Create(metaFile,
		goerofs.WithBlockSize(512),
		goerofs.WithDataFile(dataFile),
	)

	if err := tarconv.Apply(w, r); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: apply tar: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: finalise EROFS: %w", err)
	}

	// Assemble the combined output: [EROFS metadata][payload data].
	out, err := os.Create(layerPath)
	if err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: create output: %w", err)
	}
	defer out.Close()

	if _, err := metaFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: seek meta: %w", err)
	}
	if _, err := io.Copy(out, metaFile); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: copy meta: %w", err)
	}

	if _, err := dataFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: seek data: %w", err)
	}
	if _, err := io.Copy(out, dataFile); err != nil {
		return fmt.Errorf("GenerateTarIndexAndAppendTar: append data: %w", err)
	}

	log.G(ctx).Debugf("GenerateTarIndexAndAppendTar: wrote tar-index EROFS at %s", layerPath)
	return nil
}

// ConvertErofs converts a source directory srcDir into an EROFS image at
// layerPath using the pure-Go go-erofs writer.
//
// On platforms where go-erofs cannot extract Unix metadata from os.Stat()
// results (i.e., non-Linux, non-Darwin), file ownership and device numbers
// will be zero-valued. Permission bits and timestamps are always preserved.
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
	if err := w.Close(); err != nil {
		return fmt.Errorf("ConvertErofs: finalise EROFS: %w", err)
	}
	log.G(ctx).Debugf("ConvertErofs: wrote %s from %s", layerPath, srcDir)
	return nil
}

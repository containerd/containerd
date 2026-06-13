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

// Package erofsutils_test provides benchmarks and tests for the pure-Go EROFS
// conversion functions.
//
// # Running benchmarks
//
//	go test ./internal/erofsutils/... -bench=. -benchtime=3x -v
package erofsutils_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/internal/erofsutils"
	goerofs "github.com/erofs/go-erofs"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Helpers
// ============================================================

// makeTarStream builds a synthetic tar archive of approximately payloadBytes
// bytes of content. The archive contains a flat directory tree with one file
// per subdirectory, each file containing repeating 4 KiB payloads.
func makeTarStream(t testing.TB, payloadBytes int) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: "./", Mode: 0755,
	}))

	fileData := bytes.Repeat([]byte("deadbeef"), 512) // 4096 bytes each
	count := payloadBytes / len(fileData)
	if count == 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		dir := "d" + string(rune('a'+i%26))
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir, Name: dir + "/", Mode: 0755,
		}))
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     dir + "/file",
			Size:     int64(len(fileData)),
			Mode:     0644,
		}))
		_, err := tw.Write(fileData)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// makeTarStreamOCI builds a small but realistic OCI-shaped layer tar.
func makeTarStreamOCI(t testing.TB) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	writeDir := func(name string) {
		t.Helper()
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir, Name: name + "/", Mode: 0755,
		}))
	}
	writeFile := func(name string, data []byte, mode int64) {
		t.Helper()
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Size: int64(len(data)), Mode: mode,
		}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}

	writeDir(".")
	writeDir("etc")
	writeFile("etc/hostname", []byte("benchhost"), 0644)
	writeDir("usr")
	writeDir("usr/bin")
	writeFile("usr/bin/sh", bytes.Repeat([]byte{0xEF}, 65536), 0755)
	writeDir("var")
	writeDir("var/log")
	writeFile("var/log/app.log", bytes.Repeat([]byte("log\n"), 4096), 0644)

	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// makeSourceDir builds a directory tree of approximately payloadBytes bytes.
func makeSourceDir(t testing.TB, payloadBytes int) string {
	t.Helper()
	dir := t.TempDir()
	fileData := bytes.Repeat([]byte("deadbeef"), 512)
	count := payloadBytes / len(fileData)
	if count == 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		sub := filepath.Join(dir, "d"+string(rune('a'+i%26)))
		require.NoError(t, os.MkdirAll(sub, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "file"), fileData, 0644))
	}
	return dir
}

// ============================================================
// Benchmarks
// ============================================================

func BenchmarkConvertTarErofs_Go_1MB(b *testing.B)  { benchTarGo(b, 1<<20) }
func BenchmarkConvertTarErofs_Go_16MB(b *testing.B) { benchTarGo(b, 16<<20) }
func BenchmarkConvertTarErofs_Go_64MB(b *testing.B) { benchTarGo(b, 64<<20) }

func benchTarGo(b *testing.B, size int) {
	data := makeTarStream(b, size)
	ctx := context.Background()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(b.TempDir(), "layer.erofs")
		if err := erofsutils.ConvertTarErofs(ctx, bytes.NewReader(data), out, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertDirErofs_Go_1MB(b *testing.B)  { benchDirGo(b, 1<<20) }
func BenchmarkConvertDirErofs_Go_16MB(b *testing.B) { benchDirGo(b, 16<<20) }

func benchDirGo(b *testing.B, size int) {
	src := makeSourceDir(b, size)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(b.TempDir(), "layer.erofs")
		if err := erofsutils.ConvertErofs(ctx, out, src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOCILayer_Go(b *testing.B) {
	data := makeTarStreamOCI(b)
	ctx := context.Background()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(b.TempDir(), "layer.erofs")
		if err := erofsutils.ConvertTarErofs(ctx, bytes.NewReader(data), out, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// ============================================================
// Correctness tests
// ============================================================

// TestConvertTarErofsBasic verifies that ConvertTarErofs produces a valid
// EROFS image (confirmed by the EROFS superblock magic at offset 1024).
func TestConvertTarErofsBasic(t *testing.T) {
	data := makeTarStream(t, 64*1024)
	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertTarErofs(context.Background(),
		bytes.NewReader(data), out, ""))
	checkSuperblock(t, out)
}

// TestConvertErofsBasic verifies that ConvertErofs produces a valid
// EROFS image.
func TestConvertErofsBasic(t *testing.T) {
	src := makeSourceDir(t, 64*1024)
	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertErofs(context.Background(), out, src))
	checkSuperblock(t, out)
}

// TestConvertTarErofsWhiteouts verifies that OCI whiteout entries (.wh.*)
// are translated to overlayfs char-device nodes in the pure-Go path.
func TestConvertTarErofsWhiteouts(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "./", Mode: 0755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "keep.txt", Size: 4, Mode: 0644}))
	_, err := tw.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: ".wh.gone.txt", Size: 0, Mode: 0644}))
	require.NoError(t, tw.Close())

	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertTarErofs(context.Background(),
		bytes.NewReader(buf.Bytes()), out, ""))
	checkSuperblock(t, out)

	names := erofsFileNames(t, out)
	var foundKeep, foundGone bool
	for _, n := range names {
		switch strings.TrimPrefix(n, "./") {
		case "keep.txt":
			foundKeep = true
		case "gone.txt":
			foundGone = true
		}
	}
	require.True(t, foundKeep, "keep.txt must be present: %v", names)
	require.True(t, foundGone, "gone.txt whiteout must appear as a device node: %v", names)
}

// ============================================================
// Tar-index tests
// ============================================================

// TestGenerateTarIndexAndAppendTarBasic verifies that
// GenerateTarIndexAndAppendTar produces a file with a valid EROFS
// superblock at offset 1024 and that the output is larger than the metadata
// alone (confirming the data region is appended).
func TestGenerateTarIndexAndAppendTarBasic(t *testing.T) {
	data := makeTarStream(t, 32*1024)
	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.GenerateTarIndexAndAppendTar(
		context.Background(), bytes.NewReader(data), out, ""))

	checkSuperblock(t, out)

	fi, err := os.Stat(out)
	require.NoError(t, err)
	require.Greater(t, fi.Size(), int64(4096),
		"tar-index output must contain metadata + data region")
}

// TestGenerateTarIndexAndAppendTarFileList verifies that the EROFS metadata
// section of the tar-index output lists the same files as a full-extraction
// EROFS image produced from the same tar stream.
func TestGenerateTarIndexAndAppendTarFileList(t *testing.T) {
	data := makeTarStream(t, 32*1024)

	// Full extraction for comparison.
	fullOut := filepath.Join(t.TempDir(), "full.erofs")
	require.NoError(t, erofsutils.ConvertTarErofs(
		context.Background(), bytes.NewReader(data), fullOut, ""))

	// Tar-index output.
	idxOut := filepath.Join(t.TempDir(), "idx.erofs")
	require.NoError(t, erofsutils.GenerateTarIndexAndAppendTar(
		context.Background(), bytes.NewReader(data), idxOut, ""))

	fullNames := erofsFileNames(t, fullOut)
	// The tar-index EROFS image references DeviceID=1 (the data region
	// appended after the metadata in the same file). Pass the file itself
	// as the extra device so go-erofs can resolve chunk references.
	idxNames := erofsFileNamesWithDevice(t, idxOut, idxOut)

	require.Equal(t, len(fullNames), len(idxNames),
		"tar-index and full-extraction must have the same number of entries")
}

// TestGenerateTarIndexAndAppendTarPayload verifies that file payload bytes
// are present somewhere in the combined output.
func TestGenerateTarIndexAndAppendTarPayload(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: "./", Mode: 0755,
	}))
	const payload = "hello tar-index world"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "hello.txt",
		Size: int64(len(payload)), Mode: 0644,
	}))
	_, err := tw.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.GenerateTarIndexAndAppendTar(
		context.Background(), bytes.NewReader(buf.Bytes()), out, ""))

	checkSuperblock(t, out)

	outData, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Contains(t, string(outData), payload,
		"file payload must be present in the tar-index output")
}

// ============================================================
// Test helpers
// ============================================================

// checkSuperblock asserts that the file at path contains the EROFS superblock
// magic bytes (0xE0F5E1E2 in little-endian) at offset 1024.
func checkSuperblock(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	fi, err := f.Stat()
	require.NoError(t, err)
	require.Greater(t, fi.Size(), int64(1028), "image too small for superblock")

	var magic [4]byte
	_, err = f.ReadAt(magic[:], 1024)
	require.NoError(t, err)
	require.Equal(t, [4]byte{0xE2, 0xE1, 0xF5, 0xE0}, magic,
		"EROFS magic 0xE0F5E1E2 must be present at offset 1024")
}

// erofsFileNames opens an EROFS image with go-erofs and returns the path of
// every entry as returned by fs.WalkDir.
func erofsFileNames(t *testing.T, path string) []string {
	t.Helper()
	return erofsFileNamesWithDevice(t, path, "")
}

// erofsFileNamesWithDevice opens an EROFS image with go-erofs, optionally
// supplying an extra device for chunk-based images created with WithDataFile.
// Pass devicePath == "" when the image is self-contained.
func erofsFileNamesWithDevice(t *testing.T, path, devicePath string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var opts []goerofs.OpenOpt
	if devicePath != "" {
		dev, err := os.Open(devicePath)
		if err == nil {
			defer dev.Close()
			opts = append(opts, goerofs.WithExtraDevices(dev))
		}
	}

	fsys, err := goerofs.Open(f, opts...)
	require.NoError(t, err)

	var names []string
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		names = append(names, p)
		return nil
	})
	require.NoError(t, err)
	return names
}

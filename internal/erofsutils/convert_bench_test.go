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
	"fmt"
	"io"
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
		dir := fmt.Sprintf("d%06d", i)
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
		sub := filepath.Join(dir, fmt.Sprintf("d%06d", i))
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

// TestConvertTarErofsIndexLayout verifies that ConvertTarErofsIndex writes a
// metadata image that declares one data device and keeps file payload out of
// the image, and a data device that holds the payload.
func TestConvertTarErofsIndexLayout(t *testing.T) {
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

	dir := t.TempDir()
	layerPath := filepath.Join(dir, "layer.erofs")
	devicePath := erofsutils.DevicePath(dir, 0)
	require.NoError(t, erofsutils.ConvertTarErofsIndex(
		context.Background(), bytes.NewReader(buf.Bytes()), layerPath, devicePath, ""))

	checkSuperblock(t, layerPath)
	metaData, err := os.ReadFile(layerPath)
	require.NoError(t, err)
	require.NotContains(t, string(metaData), payload,
		"payload must not appear inside the metadata image")

	deviceData, err := os.ReadFile(devicePath)
	require.NoError(t, err)
	require.Contains(t, string(deviceData), payload,
		"payload must appear in the data device")
	require.Zero(t, len(deviceData)%512, "data device must be a whole number of 512-byte blocks")

	// Opening the image without its device fails: it declares one.
	f, err := os.Open(layerPath)
	require.NoError(t, err)
	defer f.Close()
	_, err = goerofs.Open(f)
	require.Error(t, err, "metadata image must declare its data device")

	// Only the two outputs are left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"layer.erofs", "device-0.blob"}, names)
}

// TestConvertTarErofsIndexContent verifies that the metadata image, read with
// its data device attached, has the same files and contents as a full EROFS
// image converted from the same tar stream.
func TestConvertTarErofsIndexContent(t *testing.T) {
	data := makeTarStream(t, 32*1024)

	fullOut := filepath.Join(t.TempDir(), "full.erofs")
	require.NoError(t, erofsutils.ConvertTarErofs(
		context.Background(), bytes.NewReader(data), fullOut, ""))

	dir := t.TempDir()
	layerPath := filepath.Join(dir, "layer.erofs")
	devicePath := erofsutils.DevicePath(dir, 0)
	require.NoError(t, erofsutils.ConvertTarErofsIndex(
		context.Background(), bytes.NewReader(data), layerPath, devicePath, ""))

	require.Equal(t, erofsContents(t, fullOut), erofsContents(t, layerPath, devicePath))
}

// TestConvertTarErofsIndexNoFileData verifies that a layer without file data
// still gets a data device that can be attached and formatted: one zero block
// rather than an empty file.
func TestConvertTarErofsIndexNoFileData(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "dir/", Mode: 0755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "dir/empty", Mode: 0644}))
	require.NoError(t, tw.Close())

	dir := t.TempDir()
	layerPath := filepath.Join(dir, "layer.erofs")
	devicePath := erofsutils.DevicePath(dir, 0)
	require.NoError(t, erofsutils.ConvertTarErofsIndex(
		context.Background(), bytes.NewReader(buf.Bytes()), layerPath, devicePath, ""))

	fi, err := os.Stat(devicePath)
	require.NoError(t, err)
	require.Equal(t, int64(512), fi.Size())
	require.Equal(t, map[string]string{".": "", "dir": "", "dir/empty": ""}, erofsContents(t, layerPath, devicePath))
}

// TestLayerDevices verifies that LayerDevices returns the data devices in
// order, stops at the first missing index, and rejects a non-regular device.
func TestLayerDevices(t *testing.T) {
	dir := t.TempDir()
	devices, err := erofsutils.LayerDevices(dir)
	require.NoError(t, err)
	require.Empty(t, devices)

	for _, n := range []int{0, 1, 3} {
		require.NoError(t, os.WriteFile(erofsutils.DevicePath(dir, n), []byte("x"), 0644))
	}
	devices, err = erofsutils.LayerDevices(dir)
	require.NoError(t, err)
	require.Equal(t, []string{erofsutils.DevicePath(dir, 0), erofsutils.DevicePath(dir, 1)}, devices)

	require.NoError(t, os.Remove(erofsutils.DevicePath(dir, 1)))
	require.NoError(t, os.Mkdir(erofsutils.DevicePath(dir, 1), 0755))
	_, err = erofsutils.LayerDevices(dir)
	require.Error(t, err)
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
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	fsys, err := goerofs.Open(f)
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

// erofsContents returns every path in the EROFS image at path mapped to its
// file contents, or to "" for a non-regular entry. Extra devices are attached
// in order.
func erofsContents(t *testing.T, path string, devices ...string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var extra []io.ReaderAt
	for _, d := range devices {
		df, err := os.Open(d)
		require.NoError(t, err)
		defer df.Close()
		extra = append(extra, df)
	}
	fsys, err := goerofs.Open(f, goerofs.WithExtraDevices(extra...))
	require.NoError(t, err)

	contents := map[string]string{}
	require.NoError(t, fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			contents[p] = ""
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		contents[p] = string(b)
		return nil
	}))
	return contents
}

//go:build linux

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

package blockfile

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopyFilePreservesSparseness(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "target")

	// Create a 1MB sparse file with data only in the first and last 4K blocks.
	const fileSize = 1 << 20
	const blockSize = 4096

	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}

	headData := make([]byte, blockSize)
	if _, err := rand.Read(headData); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(headData, 0); err != nil {
		t.Fatal(err)
	}
	tailData := make([]byte, blockSize)
	if _, err := rand.Read(tailData); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(tailData, fileSize-blockSize); err != nil {
		t.Fatal(err)
	}

	if err := f.Truncate(fileSize); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Verify source is actually sparse.
	srcStat := statBlocks(t, src)
	t.Logf("source: apparent=%d blocks=%d disk=%d", fileSize, srcStat.Blocks, srcStat.Blocks*512)
	if srcStat.Blocks*512 >= fileSize {
		t.Skip("source file is not sparse on this filesystem, skipping test")
	}

	if err := copyFileWithSync(dst, src); err != nil {
		t.Fatalf("copyFileWithSync failed: %v", err)
	}

	// Verify destination has the correct apparent size.
	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if dstInfo.Size() != fileSize {
		t.Fatalf("destination size = %d, want %d", dstInfo.Size(), fileSize)
	}

	// Verify destination is sparse (not fully allocated).
	dstStat := statBlocks(t, dst)
	t.Logf("target: apparent=%d blocks=%d disk=%d", dstInfo.Size(), dstStat.Blocks, dstStat.Blocks*512)
	if dstStat.Blocks*512 >= fileSize {
		t.Fatalf("destination is not sparse: %d blocks (%d bytes) for a %d byte file",
			dstStat.Blocks, dstStat.Blocks*512, fileSize)
	}

	srcContent, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(srcContent, dstContent) {
		t.Fatal("destination content does not match source")
	}
}

func TestCopyFileFullyAllocated(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "target")

	// Create a non-sparse file with random data.
	data := make([]byte, 32*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFileWithSync(dst, src); err != nil {
		t.Fatalf("copyFileWithSync failed: %v", err)
	}

	srcContent, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dstContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(srcContent, dstContent) {
		t.Fatal("destination content does not match source")
	}
}

func TestCopyFileEntirelySpare(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "target")

	// Create an entirely sparse file (no data at all).
	const fileSize = 1 << 20
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fileSize); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := copyFileWithSync(dst, src); err != nil {
		t.Fatalf("copyFileWithSync failed: %v", err)
	}

	dstInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if dstInfo.Size() != fileSize {
		t.Fatalf("destination size = %d, want %d", dstInfo.Size(), fileSize)
	}

	dstStat := statBlocks(t, dst)
	if dstStat.Blocks != 0 {
		t.Fatalf("expected 0 blocks for entirely sparse file, got %d", dstStat.Blocks)
	}
}

func statBlocks(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t)
}

// createSparseFile creates a 100MiB sparse file with 1MiB of random data
// at the beginning. The remaining 99MiB is a hole.
func createSparseFile(b *testing.B, path string) {
	b.Helper()
	const fileSize = 100 << 20
	const dataSize = 1 << 20

	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()

	data := make([]byte, dataSize)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		b.Fatal(err)
	}
	if err := f.Truncate(fileSize); err != nil {
		b.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		b.Fatal(err)
	}
}

func naiveCopyFile(target, source string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	tgt, err := os.Create(target)
	if err != nil {
		return err
	}
	defer tgt.Close()
	defer tgt.Sync()

	_, err = io.Copy(tgt, src)
	return err
}

func BenchmarkCopySparsefile(b *testing.B) {
	dir := b.TempDir()
	src := filepath.Join(dir, "source")
	createSparseFile(b, src)

	fi, _ := os.Stat(src)
	st := fi.Sys().(*syscall.Stat_t)
	b.Logf("source: apparent=%d blocks=%d disk=%d", fi.Size(), st.Blocks, st.Blocks*512)

	b.Run("sparse-aware", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dst := filepath.Join(dir, "dst-sparse")
			if err := copyFileWithSync(dst, src); err != nil {
				b.Fatal(err)
			}
			os.Remove(dst)
		}
	})

	b.Run("io.Copy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dst := filepath.Join(dir, "dst-naive")
			if err := naiveCopyFile(dst, src); err != nil {
				b.Fatal(err)
			}
			os.Remove(dst)
		}
	})
}

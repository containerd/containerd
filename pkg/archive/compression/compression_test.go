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

package compression

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Force initPigz to be called, so tests start with the same initial state
	gzipDecompress(context.Background(), strings.NewReader(""))
	os.Exit(m.Run())
}

// generateData generates data that composed of 2 random parts
// and single zero-filled part within them.
// Typically, the compression ratio would be about 67%.
func generateData(t testing.TB, size int) []byte {
	part0 := size / 3             // random
	part2 := size / 3             // random
	part1 := size - part0 - part2 // zero-filled
	part0Data := make([]byte, part0)
	if _, err := rand.Read(part0Data); err != nil {
		t.Fatal(err)
	}
	part1Data := make([]byte, part1)
	part2Data := make([]byte, part2)
	if _, err := rand.Read(part2Data); err != nil {
		t.Fatal(err)
	}
	return append(part0Data, append(part1Data, part2Data...)...)
}

func testCompress(t testing.TB, orig []byte, compression Compression) []byte {
	size := len(orig)
	var b bytes.Buffer
	compressor, err := CompressStream(&b, compression)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := compressor.Write(orig); err != nil || n != size {
		t.Fatal(err)
	}
	compressor.Close()

	return b.Bytes()
}

func testDecompress(t testing.TB, compressed []byte) ([]byte, DecompressReadCloser) {
	decompressor, err := DecompressStream(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(decompressor)
	if err != nil {
		t.Fatal(err)
	}
	return decompressed, decompressor
}

func testCompressDecompress(t testing.TB, size int, compression Compression) DecompressReadCloser {
	orig := generateData(t, size)
	compressed := testCompress(t, orig, compression)
	t.Logf("compressed %d bytes to %d bytes (%.2f%%)",
		len(orig), len(compressed), 100.0*float32(len(compressed))/float32(len(orig)))
	if compared := bytes.Compare(orig, compressed); (compression == Uncompressed && compared != 0) ||
		(compression != Uncompressed && compared == 0) {
		t.Fatal("strange compressed data")
	}

	decompressed, decompressor := testDecompress(t, compressed)
	if !bytes.Equal(orig, decompressed) {
		t.Fatal("strange decompressed data")
	}

	return decompressor
}

func TestCompressDecompressGzip(t *testing.T) {
	oldUnpigzPath := gzipPath
	gzipPath = ""
	defer func() { gzipPath = oldUnpigzPath }()

	decompressor := testCompressDecompress(t, 1024*1024, Gzip)
	wrapper := decompressor.(*readCloserWrapper)
	_, ok := wrapper.Reader.(*gzip.Reader)
	if !ok {
		t.Fatalf("unexpected compressor type: %T", wrapper.Reader)
	}
}

func TestCompressDecompressPigz(t *testing.T) {
	if _, err := exec.LookPath("unpigz"); err != nil {
		t.Skip("pigz not installed")
	}

	decompressor := testCompressDecompress(t, 1024*1024, Gzip)
	wrapper := decompressor.(*readCloserWrapper)
	_, ok := wrapper.Reader.(*io.PipeReader)
	if !ok {
		t.Fatalf("unexpected compressor type: %T", wrapper.Reader)
	}
}

func TestCompressDecompressUncompressed(t *testing.T) {
	testCompressDecompress(t, 1024*1024, Uncompressed)
}

func TestDetectPigz(t *testing.T) {
	// Create fake PATH with unpigz executable, make sure detectPigz can find it
	tempPath := t.TempDir()

	filename := "unpigz"
	if runtime.GOOS == "windows" {
		filename = "unpigz.exe"
	}

	fullPath := filepath.Join(tempPath, filename)

	if err := os.WriteFile(fullPath, []byte(""), 0111); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", tempPath)

	if pigzPath := detectCommand("unpigz", disablePigzEnv); pigzPath == "" {
		t.Fatal("failed to detect pigz path")
	} else if pigzPath != fullPath {
		t.Fatalf("wrong pigz found: %s != %s", pigzPath, fullPath)
	}

	t.Setenv(disablePigzEnv, "1")

	if pigzPath := detectCommand("unpigz", disablePigzEnv); pigzPath != "" {
		t.Fatalf("disable via %s doesn't work", disablePigzEnv)
	}
}

func TestCmdStream(t *testing.T) {
	out, err := cmdStream(exec.Command("sh", "-c", "echo hello; exit 0"), nil)
	if err != nil {
		t.Fatal(err)
	}

	buf, err := io.ReadAll(out)
	if err != nil {
		t.Fatalf("failed to read from stdout: %s", err)
	}

	if string(buf) != "hello\n" {
		t.Fatalf("unexpected command output ('%s' != '%s')", string(buf), "hello\n")
	}
}

func TestCmdStreamBad(t *testing.T) {
	out, err := cmdStream(exec.Command("sh", "-c", "echo hello; echo >&2 bad result; exit 1"), nil)
	if err != nil {
		t.Fatalf("failed to start command: %v", err)
	}

	if buf, err := io.ReadAll(out); err == nil {
		t.Fatal("command should have failed")
	} else if err.Error() != "exit status 1: bad result\n" {
		t.Fatalf("wrong error: %s", err.Error())
	} else if string(buf) != "hello\n" {
		t.Fatalf("wrong output: %s", string(buf))
	}
}

func TestDetectCompressionZstd(t *testing.T) {
	for _, tc := range []struct {
		source   []byte
		expected Compression
	}{
		{
			// test zstd compression without skippable frames.
			source: []byte{
				0x28, 0xb5, 0x2f, 0xfd, // magic number of Zstandard frame: 0xFD2FB528
				0x04, 0x00, 0x31, 0x00, 0x00, // frame header
				0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72, // data block "docker"
				0x16, 0x0e, 0x21, 0xc3, // content checksum
			},
			expected: Zstd,
		},
		{
			// test zstd compression with skippable frames.
			source: []byte{
				0x50, 0x2a, 0x4d, 0x18, // magic number of skippable frame: 0x184D2A50 to 0x184D2A5F
				0x04, 0x00, 0x00, 0x00, // frame size
				0x5d, 0x00, 0x00, 0x00, // user data
				0x28, 0xb5, 0x2f, 0xfd, // magic number of Zstandard frame: 0xFD2FB528
				0x04, 0x00, 0x31, 0x00, 0x00, // frame header
				0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72, // data block "docker"
				0x16, 0x0e, 0x21, 0xc3, // content checksum
			},
			expected: Zstd,
		},
	} {
		compression := DetectCompression(tc.source)
		if compression != tc.expected {
			t.Fatalf("Unexpected compression %v, expected %v", compression, tc.expected)
		}
	}
}

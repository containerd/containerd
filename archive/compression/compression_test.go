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
	"crypto/rand"
	"io/ioutil"
	"testing"
)

// generateData generates data that composed of 2 random parts
// and single zero-filled part within them.
// Typically, the compression ratio would be about 67%.
func generateData(t *testing.T, size int) []byte {
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

func testCompressDecompress(t *testing.T, size int, compression Compression) {
	orig := generateData(t, size)
	var b bytes.Buffer
	compressor, err := CompressStream(&b, compression)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := compressor.Write(orig); err != nil || n != size {
		t.Fatal(err)
	}
	compressor.Close()
	compressed := b.Bytes()
	t.Logf("compressed %d bytes to %d bytes (%.2f%%)",
		len(orig), len(compressed), 100.0*float32(len(compressed))/float32(len(orig)))
	if compared := bytes.Compare(orig, compressed); (compression == Uncompressed && compared != 0) ||
		(compression != Uncompressed && compared == 0) {
		t.Fatal("strange compressed data")
	}

	decompressor, err := DecompressStream(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := ioutil.ReadAll(decompressor)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, decompressed) {
		t.Fatal("strange decompressed data")
	}
}

func TestCompressDecompressGzip(t *testing.T) {
	testCompressDecompress(t, 1024*1024, Gzip)
}

func TestCompressDecompressUncompressed(t *testing.T) {
	testCompressDecompress(t, 1024*1024, Uncompressed)
}

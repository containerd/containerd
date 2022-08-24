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
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const benchmarkTestDataURL = "https://git.io/fADcl"

func BenchmarkDecompression(b *testing.B) {
	resp, err := http.Get(benchmarkTestDataURL)
	require.NoError(b, err)

	data, err := io.ReadAll(resp.Body)
	require.NoError(b, err)
	resp.Body.Close()

	const mib = 1024 * 1024
	sizes := []int{32, 64, 128, 256}

	for _, sizeInMiB := range sizes {
		size := sizeInMiB * mib
		for len(data) < size {
			data = append(data, data...)
		}
		data = data[0:size]

		gz := testCompress(b, data, Gzip)
		zstd := testCompress(b, data, Zstd)

		b.Run(fmt.Sprintf("size=%dMiB", sizeInMiB), func(b *testing.B) {
			b.Run("gzip", func(b *testing.B) {
				testDecompress(b, gz)
			})
			b.Run("zstd", func(b *testing.B) {
				testDecompress(b, zstd)
			})
			if unpigzPath != "" {
				original := unpigzPath
				unpigzPath = ""
				b.Run("gzipPureGo", func(b *testing.B) {
					testDecompress(b, gz)
				})
				unpigzPath = original
			}
		})
	}
}

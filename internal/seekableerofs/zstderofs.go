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

package seekableerofs

import (
	"context"
	"io"
)

// DecodeErofsAll stream-decodes the EROFS image bytes from a seekable EROFS blob.
func DecodeErofsAll(ctx context.Context, blob io.Reader, out io.Writer) (int64, error) {
	_ = ctx
	_ = blob
	_ = out
	return 0, ErrNotImplemented
}

// DecodeErofsFrame reads and decodes exactly one zstd frame from a seekable EROFS blob.
// Caller expected to obtain (frameStart, frameEnd, expectedChecksum) from the chunk table.
func DecodeErofsFrame(ctx context.Context, blob io.ReaderAt, frameStart, frameEnd int64, expectedChecksum []byte, out io.Writer) (int64, error) {
	_ = ctx
	_ = blob
	_ = frameStart
	_ = frameEnd
	_ = expectedChecksum
	_ = out
	// Read compressedFrameBytes = blob[frameStart:frameEnd]
	// If expectedChecksum is non-empty: verify SHA-512 over compressedFrameBytes before decompression.
	// Decompress that single frame and write the uncompressed bytes to out.
	return 0, ErrNotImplemented
}

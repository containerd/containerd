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

package seekable

import (
	"bytes"
	"context"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"

	"github.com/containerd/errdefs"
	"github.com/klauspost/compress/zstd"
)

// DecodeErofsAll stream-decodes the EROFS image bytes from a seekable EROFS blob.
func DecodeErofsAll(ctx context.Context, blob io.Reader, out io.Writer) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	dec, err := zstd.NewReader(blob, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return 0, fmt.Errorf("failed to create zstd decoder: %w", err)
	}
	defer dec.Close()

	bytesDecoded, err := io.Copy(out, dec)
	if err != nil {
		return bytesDecoded, fmt.Errorf("failed to decode EROFS stream: %w", err)
	}
	return bytesDecoded, nil
}

// DecodeErofsFrame reads and decodes exactly one zstd frame from a seekable EROFS blob.
// Caller is expected to obtain (frameStart, frameEnd, expectedChecksum) from the chunk table
// via ChunkTable.FrameBounds.
//
// If expectedChecksum is non-empty, the compressed frame bytes are verified during decoding
// using a TeeReader to avoid reading the frame twice.
// Only SHA-512 is currently supported as the hash algorithm.
func DecodeErofsFrame(ctx context.Context, blob io.ReaderAt, frameStart, frameEnd int64, expectedChecksum []byte, out io.Writer) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if frameStart < 0 || frameEnd < 0 || frameEnd <= frameStart {
		return 0, fmt.Errorf("invalid frame bounds [%d, %d): %w", frameStart, frameEnd, errdefs.ErrOutOfRange)
	}
	if len(expectedChecksum) > 0 && len(expectedChecksum) != sha512.Size {
		return 0, fmt.Errorf("unsupported checksum length %d (only SHA-512 %d is supported): %w", len(expectedChecksum), sha512.Size, errdefs.ErrInvalidArgument)
	}

	frameReader := io.NewSectionReader(blob, frameStart, frameEnd-frameStart)

	// If checksum verification is requested, wrap in a TeeReader so we compute the
	// hash in a single pass as the zstd decoder reads the compressed bytes, rather
	// than reading the frame twice (once for checksum, once for decompression).
	var r io.Reader = frameReader
	var hasher hash.Hash
	if len(expectedChecksum) > 0 {
		hasher = sha512.New()
		r = io.TeeReader(frameReader, hasher)
	}

	// Decompress the frame
	decoder, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return 0, fmt.Errorf("failed to create zstd decoder: %w", err)
	}
	defer decoder.Close()

	bytesDecoded, err := io.Copy(out, decoder)
	if err != nil {
		return bytesDecoded, fmt.Errorf("failed to decode zstd frame [%d, %d): %w", frameStart, frameEnd, err)
	}

	// Verify checksum after decompression has consumed all compressed bytes.
	if hasher != nil {
		if !bytes.Equal(hasher.Sum(nil), expectedChecksum) {
			return bytesDecoded, fmt.Errorf("frame checksum mismatch (SHA-512): %w", errdefs.ErrFailedPrecondition)
		}
	}

	return bytesDecoded, nil
}

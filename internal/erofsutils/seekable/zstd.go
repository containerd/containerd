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
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/containerd/errdefs"
	"github.com/klauspost/compress/zstd"
)

// Zstd skippable frame constants.
// Skippable frames allow embedding arbitrary data in a zstd stream that decoders will skip.
// Magic numbers range from 0x184D2A50 to 0x184D2A5F (16 possible values).
// See: https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md#skippable-frames
const (
	zstdSkippableMagicMin        uint32 = 0x184D2A50
	zstdSkippableMagicMax        uint32 = 0x184D2A5F
	zstdSkippableHeaderSizeBytes        = 8 // 4 bytes magic + 4 bytes payload size
)

// isZstdSkippableMagic returns true if m is a valid zstd skippable frame magic number.
func isZstdSkippableMagic(m uint32) bool {
	return m >= zstdSkippableMagicMin && m <= zstdSkippableMagicMax
}

// readSkippableFrameHeader reads the 8-byte zstd skippable frame header at the given offset.
// Returns the magic number and payload size (not including the 8-byte header).
func readSkippableFrameHeader(r io.ReaderAt, offset int64) (magic uint32, payloadSize uint32, err error) {
	if offset < 0 {
		return 0, 0, fmt.Errorf("invalid skippable frame offset %d: %w", offset, errdefs.ErrOutOfRange)
	}
	var buf [zstdSkippableHeaderSizeBytes]byte
	if _, err := r.ReadAt(buf[:], offset); err != nil {
		return 0, 0, fmt.Errorf("failed to read skippable frame header at offset %d: %w", offset, err)
	}
	return binary.LittleEndian.Uint32(buf[0:4]), binary.LittleEndian.Uint32(buf[4:8]), nil
}

// writeSkippableFrameHeader writes an 8-byte zstd skippable frame header.
// The header consists of the magic number followed by the payload size.
func writeSkippableFrameHeader(w io.Writer, magic uint32, payloadSize uint32) error {
	var buf [zstdSkippableHeaderSizeBytes]byte
	binary.LittleEndian.PutUint32(buf[0:4], magic)
	binary.LittleEndian.PutUint32(buf[4:8], payloadSize)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("failed to write skippable frame header: %w", err)
	}
	return nil
}

// writeZstdFrame compresses data as a single zstd frame and returns the SHA-512 checksum
// of the compressed output. The encoder is Reset'd (not re-allocated) for each call.
func writeZstdFrame(enc *zstd.Encoder, w io.Writer, data []byte) (checksum []byte, err error) {
	hasher := sha512.New()
	// MultiWriter writes compressed bytes to both the output stream and the hasher
	// simultaneously, so we get the checksum without needing to re-read the compressed data.
	multiWriter := io.MultiWriter(w, hasher)

	// Reset reuses all internal buffers and avoids per-frame allocation.
	// See: https://pkg.go.dev/github.com/klauspost/compress/zstd#Encoder.Reset
	enc.Reset(multiWriter)

	if _, writeErr := enc.Write(data); writeErr != nil {
		_ = enc.Close() // best-effort cleanup
		return nil, fmt.Errorf("failed to write zstd frame: %w", writeErr)
	}
	// Close flushes remaining data to the writer before we compute the checksum.
	// The encoder can still be reused after Close via Reset.
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("failed to close zstd frame: %w", err)
	}
	return hasher.Sum(nil), nil
}

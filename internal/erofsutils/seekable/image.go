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
	"context"
	"fmt"
	"io"
	"os"

	"github.com/containerd/errdefs"
	"github.com/klauspost/compress/zstd"
)

// Defaults for `ctr images convert --erofs-seekable`
const (
	// DefaultChunkSize is the default uncompressed bytes per zstd frame
	// This is the random access granularity
	DefaultChunkSize = 4 * 1024 * 1024 // 4 MiB

	// DefaultDMVerityBlockSize is the default data block size in bytes used by dm-verity.
	DefaultDMVerityBlockSize = 4096 // 4 KiB
)

type EncodeOptions struct {
	ChunkSizeBytes    int
	IncludeDMVerity   bool
	DMVerityBlockSize int // Required when IncludeDMVerity is true.
}

type EncodeResult struct {
	ChunkTableOffset   int64
	ChunkTableDigest   string // OCI digest format: "sha512:<hex>"
	HasDMVerity        bool
	DMVerityOffset     int64
	DMVerityRootDigest string
}

// EncodeErofsImageTo encodes a raw EROFS image into the seekable EROFS blob format.
//
// On-disk layout:
//   - normal zstd frames for EROFS bytes (one frame per uncompressed chunk)
//   - chunk table as a zstd skippable frame
//   - optional dm-verity payload as a zstd skippable frame at EOF
//
// [normal zstd frames: chunked EROFS image] [skippable: chunk table] [skippable: dm-verity data (optional)]
func EncodeErofsImageTo(ctx context.Context, erofsImage io.Reader, out io.Writer, opts EncodeOptions) (EncodeResult, error) {
	//  EROFS section
	if opts.ChunkSizeBytes <= 0 {
		return EncodeResult{}, fmt.Errorf("invalid chunk size %d (must be > 0): %w", opts.ChunkSizeBytes, errdefs.ErrInvalidArgument)
	}

	// DM-Verity validation
	if opts.IncludeDMVerity && !IsValidDMVerityBlockSize(opts.DMVerityBlockSize) {
		return EncodeResult{}, fmt.Errorf("invalid dm-verity block size %d: %w", opts.DMVerityBlockSize, errdefs.ErrInvalidArgument)
	}

	// Wrap output in a countingWriter to track byte offsets. This is necessary because
	// content.Writer (the containerd content store) doesn't implement io.Seeker, so we
	// can't query the current position. We need offsets for the chunk table entries
	// and dm-verity frame location annotations.
	w := &countingWriter{w: out}

	// When dm-verity is enabled, we write the raw EROFS bytes to a temp file in addition
	// to the compressed output. This is required because the dm-verity library needs to
	// read the uncompressed data to compute the Merkle tree, and we're streaming the
	// compressed output (can't seek back to re-read).
	var erofsTmpPath string
	var erofsTmpFile *os.File
	if opts.IncludeDMVerity {
		f, err := os.CreateTemp("", "erofs-raw-*.bin")
		if err != nil {
			return EncodeResult{}, fmt.Errorf("failed to create temp erofs file: %w", err)
		}
		erofsTmpPath = f.Name()
		erofsTmpFile = f
	}
	defer func() {
		if erofsTmpFile != nil {
			erofsTmpFile.Close()
		}
		if erofsTmpPath != "" {
			_ = os.Remove(erofsTmpPath)
		}
	}()

	// Encode EROFS image as chunked zstd frames.
	totalIn, entries, err := encodeChunkedFrames(ctx, erofsImage, w, opts.ChunkSizeBytes, erofsTmpFile)
	if err != nil {
		return EncodeResult{}, err
	}

	// Chunk Table section
	// Write chunk table skippable frame immediately after the data frames.
	tbl := ChunkTable{
		Header: ChunkTableHeader{
			UncompressedSizeBytes: uint64(totalIn),
			ChunkSizeBytes:        uint32(opts.ChunkSizeBytes),
			HashAlgo:              chunkHashAlgoSHA512,
			HashSize:              64, // SHA-512
		},
		Entries: entries,
	}
	chunkTableOffset, _, chunkTableDigest, err := writeChunkTable(ctx, w, tbl)
	if err != nil {
		return EncodeResult{}, fmt.Errorf("failed to write chunk table: %w", err)
	}

	// DM Verity section
	var hasDMVerity bool
	var dmVerityFrameOffset int64
	var dmVerityRootDigest string
	if opts.IncludeDMVerity {
		// Close the temp file handle before dm-verity computation. The go-dmverity
		// library (verity.Create) needs to open the file itself to pad it to block
		// alignment and compute the Merkle tree. We can't keep our handle open.
		if err := erofsTmpFile.Close(); err != nil {
			return EncodeResult{}, fmt.Errorf("failed to close temp erofs file: %w", err)
		}
		erofsTmpFile = nil

		frameOffset, rootDigest, _, err := WriteDMVeritySkippableFrameAtEOF(ctx, w, erofsTmpPath, totalIn, opts.DMVerityBlockSize)
		if err != nil {
			return EncodeResult{}, fmt.Errorf("failed to write dm-verity frame: %w", err)
		}
		hasDMVerity = true
		dmVerityFrameOffset = frameOffset
		dmVerityRootDigest = rootDigest
	}

	return EncodeResult{
		ChunkTableOffset:   chunkTableOffset,
		ChunkTableDigest:   chunkTableDigest,
		HasDMVerity:        hasDMVerity,
		DMVerityOffset:     dmVerityFrameOffset,
		DMVerityRootDigest: dmVerityRootDigest,
	}, nil
}

// encodeChunkedFrames reads the EROFS image in fixed-size chunks and writes each as a zstd frame.
// If tmpFile is non-nil, it also writes the raw bytes there for dm-verity computation.
// The temp file approach is required because the go-dmverity library operates on files, not streams,
// and buffering potentially gigabyte-sized images in memory is not practical.
// Returns total uncompressed bytes read and the chunk table entries.
func encodeChunkedFrames(ctx context.Context, r io.Reader, w *countingWriter, chunkSize int, tmpFile *os.File) (totalIn int64, entries []ChunkTableEntry, err error) {
	// Create a reusable zstd encoder with concurrency=1; it is Reset'd (not
	// re-allocated) for each frame in writeZstdFrame.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	chunkBuffer := make([]byte, chunkSize)

	for {
		// Check for cancellation at the start of each chunk iteration.
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}

		// Read one chunk of uncompressed EROFS bytes.
		bytesRead, readErr := io.ReadFull(r, chunkBuffer)
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return 0, nil, fmt.Errorf("failed to read EROFS bytes: %w", readErr)
		}
		if bytesRead == 0 {
			break
		}

		totalIn += int64(bytesRead)

		// Write to temp file for dm-verity if needed.
		if tmpFile != nil {
			if _, err := tmpFile.Write(chunkBuffer[:bytesRead]); err != nil {
				return 0, nil, fmt.Errorf("failed to write temp erofs file: %w", err)
			}
		}

		// Compress and write frame.
		frameStart := w.Offset()
		checksum, err := writeZstdFrame(enc, w, chunkBuffer[:bytesRead])
		if err != nil {
			return 0, nil, err
		}

		entries = append(entries, ChunkTableEntry{
			BlockOffset: frameStart,
			Checksum:    checksum,
		})

		if readErr == io.ErrUnexpectedEOF {
			break // Last partial chunk processed
		}
	}

	return totalIn, entries, nil
}

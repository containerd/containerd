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

/*

Chunk Mapping Table Header
┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Magic                 │ 4            │ 0xCDE4EC67                                    │
│ Version               │ 4            │ 1                                             │
│ Uncompressed Size     │ 8            │ total uncompressed bytes                      │
│ Chunk Size            │ 4            │ uncompressed bytes per chunk                  │
│ Hash Algo             │ 1            │ 0=None, 1=SHA-256, 2=SHA-51                   │
│ Hash Size             │ 1            │ 0 if HashAlgo=0; 32 if SHA-256; 64 if SHA-512 │
│ Reserved              │ 2            │ must be 0                                     │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘

Chunk Entry (repeated)
┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Block Offset          │ 8            │ absolute file offset of chunk zstd            │
│                       │              │ frame start (points at zstd magic)            │
│ Checksum              │ N            │ optional; N = Hash Size (e.g., 32 or 64)      │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘
*/

const (
	chunkTableMagic   uint32 = 0xCDE4EC67
	chunkTableVersion uint32 = 1

	chunkHashAlgoNone   uint32 = 0
	chunkHashAlgoSHA256 uint32 = 1
	chunkHashAlgoSHA512 uint32 = 2

	chunkHashSizeNone   uint32 = 0
	chunkHashSizeSHA256 uint32 = 32
	chunkHashSizeSHA512 uint32 = 64
)

type ChunkTable struct {
	Header  ChunkTableHeader
	Entries []ChunkTableEntry
}

type ChunkTableHeader struct {
	UncompressedSizeBytes uint64
	ChunkSizeBytes        uint32
	HashAlgo              uint32
	HashSizeBytes         uint32
}

// ChunkTableEntry represents the compressed EROFS zstd frame at the corresponding index.
type ChunkTableEntry struct {
	// BlockOffset is the offset in the compressed stream that the corresponding frame starts at
	BlockOffset uint64

	// Checksum is the [optional] checksum of the corresponding frame,compressed via HashAlgo
	// When HashAlgo is none, this is empty.
	Checksum []byte
}

// FrameBounds returns (frameStart, frameEnd, checksum) for entry i.
func (t *ChunkTable) FrameBounds(i int, endOfChunks int64) (frameStart, frameEnd int64, checksum []byte, err error) {
	_ = t
	_ = i
	_ = endOfChunks
	return 0, 0, nil, ErrNotImplemented
}

// ReadChunkTable reads and parses the chunk table skippable frame located at chunkTableOffset.
func ReadChunkTable(ctx context.Context, blob io.ReaderAt, chunkTableOffset int64) (*ChunkTable, error) {
	_ = ctx
	_ = blob
	_ = chunkTableOffset
	return nil, ErrNotImplemented
}

// WriteChunkTable writes a chunk table skippable frame and returns the offset and bytes written.
func WriteChunkTable(ctx context.Context, out io.Writer, tbl ChunkTable) (chunkTableOffset int64, bytesWritten int64, err error) {
	_ = ctx
	_ = out
	_ = tbl
	return 0, 0, ErrNotImplemented
}

// computeFrameChecksumSHA512 computes SHA-512 over the entire compressed zstd frame bytes.
// This helper is internal-only and currently hard-coded to SHA-512 (TODO: support other algorithms).
func computeFrameChecksumSHA512(frameBytes []byte) ([]byte, error) {
	_ = frameBytes
	return nil, ErrNotImplemented
}

// verifyFrameChecksumSHA512 verifies SHA-512 over the entire compressed zstd frame bytes.
// This helper is internal-only and currently hard-coded to SHA-512 (TODO: support other algorithms).
func verifyFrameChecksumSHA512(frameBytes []byte, expected []byte) error {
	_ = frameBytes
	_ = expected
	return ErrNotImplemented
}

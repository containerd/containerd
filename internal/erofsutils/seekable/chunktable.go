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
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
)

/*
Chunk Mapping Table Header (on-disk format)
┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Magic                 │ 4            │ 0xCDE4EC67                                    │
│ Version               │ 4            │ 1                                             │
│ Uncompressed Size     │ 8            │ total uncompressed bytes                      │
│ Chunk Size            │ 4            │ uncompressed bytes per chunk                  │
│ Hash Algo             │ 1            │ 0=None, 1=SHA-512 (only these are supported)  │
│ Reserved              │ 2            │ must be 0                                     │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘

Chunk Entry (repeated)
┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Block Offset          │ 8            │ absolute file offset of chunk zstd            │
│                       │              │ frame start (points at zstd magic)            │
│ Checksum              │ N            │ optional; N = 64 for SHA-512                  │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘
*/

const (
	chunkTableMagic   uint32 = 0xCDE4EC67
	chunkTableVersion uint32 = 1

	// Hash algorithms for chunk checksums.
	// Only None (no checksums) and SHA-512 are supported.
	chunkHashAlgoNone   uint32 = 0
	chunkHashAlgoSHA512 uint32 = 1

	// chunkTableHeaderSizeBytes is the fixed header size:
	// magic(4) + version(4) + uncompressedSize(8) + chunkSize(4) + hashAlgo(1) + reserved(2)
	chunkTableHeaderSizeBytes = 23
)

// hashAlgoSizes maps valid hash algorithms to their checksum size in bytes.
// Returns 0 for unknown algorithms (which are rejected during validation).
var hashAlgoSizes = map[uint32]uint32{
	chunkHashAlgoNone:   0,
	chunkHashAlgoSHA512: 64,
}

type ChunkTable struct {
	Header  ChunkTableHeader
	Entries []ChunkTableEntry
}

type ChunkTableHeader struct {
	// UncompressedSizeBytes is the total size of the raw EROFS image before compression.
	UncompressedSizeBytes uint64
	// The fixed size chunks that the erofs data is split into.
	ChunkSizeBytes uint32
	// The hash algorithm used to compute the checksum of the chunks.
	HashAlgo uint32
	// HashSize is the checksum size in bytes, derived from HashAlgo.
	// This field is computed on read/create and not stored on disk.
	HashSize uint32
}

// ChunkTableEntry represents the compressed EROFS zstd frame at the corresponding index.
type ChunkTableEntry struct {
	// BlockOffset is the byte offset in the compressed blob where this zstd frame starts.
	// Stored as uint64 on disk to represent the offset clearly, but converted to int64
	// in memory for compatibility with Go's io.SectionReader & other stdlib I/O interfaces.
	BlockOffset int64

	// Checksum is the [optional] checksum of the corresponding frame, computed via HashAlgo.
	// When HashAlgo is none, this is empty.
	Checksum []byte
}

// ReadChunkTable reads and parses the chunk table skippable frame located at chunkTableOffset.
// If expectedDigest is non-empty, the payload integrity is verified before parsing.
// The digest format is "sha512:<hex>".
func ReadChunkTable(ctx context.Context, blob io.ReaderAt, chunkTableOffset int64, expectedDigest string) (*ChunkTable, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if blob == nil {
		return nil, fmt.Errorf("chunk table blob reader is nil: %w", errdefs.ErrInvalidArgument)
	}
	if chunkTableOffset < 0 {
		return nil, fmt.Errorf("invalid chunk table offset %d: %w", chunkTableOffset, errdefs.ErrOutOfRange)
	}

	// Read and validate skippable frame header.
	skippableMagic, skippablePayloadSize, err := readSkippableFrameHeader(blob, chunkTableOffset)
	if err != nil {
		return nil, err
	}
	if !isZstdSkippableMagic(skippableMagic) {
		return nil, fmt.Errorf("chunk table does not start with zstd skippable magic: 0x%08x: %w", skippableMagic, errdefs.ErrInvalidArgument)
	}

	// Read entire payload (chunk table header + entries) to verify the digest
	payloadBytes := make([]byte, skippablePayloadSize)
	if _, err := blob.ReadAt(payloadBytes, chunkTableOffset+zstdSkippableHeaderSizeBytes); err != nil {
		return nil, fmt.Errorf("failed to read chunk table payload: %w", err)
	}

	// Verify integrity before parsing if digest is provided.
	if expectedDigest != "" {
		if err := verifyChunkTableIntegrity(payloadBytes, expectedDigest); err != nil {
			return nil, err
		}
	}

	chunkTableReader := bytes.NewReader(payloadBytes)

	// Parse chunk table header.
	header, err := parseChunkTableHeader(chunkTableReader)
	if err != nil {
		return nil, err
	}

	// Validate payload size matches expected size.
	entryCount := numChunks(header.UncompressedSizeBytes, header.ChunkSizeBytes)
	entrySize := 8 + header.HashSize
	expectedPayloadSize := uint32(chunkTableHeaderSizeBytes) + uint32(entryCount)*entrySize
	if skippablePayloadSize != expectedPayloadSize {
		return nil, fmt.Errorf("chunk table payload size mismatch: got %d, expected %d: %w", skippablePayloadSize, expectedPayloadSize, errdefs.ErrInvalidArgument)
	}

	// Read chunk entries.
	entries, err := readChunkEntries(chunkTableReader, entryCount, header.HashSize)
	if err != nil {
		return nil, err
	}

	return &ChunkTable{Header: header, Entries: entries}, nil
}

// verifyChunkTableIntegrity verifies the chunk table payload against the expected digest.
// The expected digest format is "sha512:<hex>" (OCI digest format).
func verifyChunkTableIntegrity(payload []byte, expectedDigest string) error {
	expected, err := digest.Parse(expectedDigest)
	if err != nil {
		return fmt.Errorf("invalid digest %q: %w: %w", expectedDigest, err, errdefs.ErrInvalidArgument)
	}
	if expected.Algorithm() != digest.SHA512 {
		return fmt.Errorf("unsupported digest algorithm %q (only sha512 supported): %w", expected.Algorithm(), errdefs.ErrInvalidArgument)
	}

	actual := digest.SHA512.FromBytes(payload)
	if actual != expected {
		return fmt.Errorf("chunk table integrity check failed: digest mismatch: %w", errdefs.ErrFailedPrecondition)
	}
	return nil
}

// FrameBounds returns (frameStart, frameEnd, checksum) for chunk entry i.
// The endOfChunks parameter is the byte offset where data frames end (typically the chunk table offset).
func (t *ChunkTable) FrameBounds(i int, endOfChunks int64) (frameStart, frameEnd int64, checksum []byte, err error) {
	if err := t.validateFrameBoundsArgs(i, endOfChunks); err != nil {
		return 0, 0, nil, err
	}

	start := t.Entries[i].BlockOffset

	// End is either the next entry's offset or endOfChunks for the last entry.
	end := endOfChunks
	if i+1 < len(t.Entries) {
		end = t.Entries[i+1].BlockOffset
	}

	if err := validateFrameRange(i, start, end, endOfChunks); err != nil {
		return 0, 0, nil, err
	}

	return start, end, t.Entries[i].Checksum, nil
}

func (t *ChunkTable) validateFrameBoundsArgs(i int, endOfChunks int64) error {
	if t == nil {
		return fmt.Errorf("chunk table is nil: %w", errdefs.ErrInvalidArgument)
	}
	if i < 0 || i >= len(t.Entries) {
		return fmt.Errorf("chunk index %d out of range (entries=%d): %w", i, len(t.Entries), errdefs.ErrOutOfRange)
	}
	if endOfChunks < 0 {
		return fmt.Errorf("invalid end-of-chunks offset %d: %w", endOfChunks, errdefs.ErrOutOfRange)
	}
	return nil
}

func validateFrameRange(i int, start, end, endOfChunks int64) error {
	if start > endOfChunks {
		return fmt.Errorf("chunk %d block offset %d out of bounds (end-of-chunks=%d): %w", i, start, endOfChunks, errdefs.ErrOutOfRange)
	}
	if end > endOfChunks {
		return fmt.Errorf("chunk %d end offset %d out of bounds (end-of-chunks=%d): %w", i, end, endOfChunks, errdefs.ErrOutOfRange)
	}
	return nil
}

/*
parseChunkTableHeader reads and validates the fixed-size chunk table header.

┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Magic                 │ 4            │ 0xCDE4EC67                                    │
│ Version               │ 4            │ 1                                             │
│ Uncompressed Size     │ 8            │ total uncompressed bytes                      │
│ Chunk Size            │ 4            │ uncompressed bytes per chunk                  │
│ Hash Algo             │ 1            │ 0=None, 1=SHA-512 (only these are supported)  │
│ Reserved              │ 2            │ must be 0                                     │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘
*/
func parseChunkTableHeader(r io.Reader) (ChunkTableHeader, error) {
	// Read the fixed-size header
	var hdrBuf [chunkTableHeaderSizeBytes]byte
	if _, err := io.ReadFull(r, hdrBuf[:]); err != nil {
		return ChunkTableHeader{}, fmt.Errorf("failed to read chunk table header: %w", err)
	}

	// Validate the magic and version
	if got := binary.LittleEndian.Uint32(hdrBuf[0:4]); got != chunkTableMagic {
		return ChunkTableHeader{}, fmt.Errorf("chunk table magic mismatch: expected 0x%08x, got 0x%08x: %w", chunkTableMagic, got, errdefs.ErrInvalidArgument)
	}
	if got := binary.LittleEndian.Uint32(hdrBuf[4:8]); got != chunkTableVersion {
		return ChunkTableHeader{}, fmt.Errorf("chunk table version mismatch: expected %d, got %d: %w", chunkTableVersion, got, errdefs.ErrInvalidArgument)
	}

	// Read the uncompressed size, chunk size, hash algorithm
	uncompressedSize := binary.LittleEndian.Uint64(hdrBuf[8:16])
	chunkSize := binary.LittleEndian.Uint32(hdrBuf[16:20])
	if chunkSize == 0 {
		return ChunkTableHeader{}, fmt.Errorf("chunk table chunk size is 0: %w", errdefs.ErrInvalidArgument)
	}
	hashAlgo := uint32(hdrBuf[20])
	reserved := binary.LittleEndian.Uint16(hdrBuf[21:23])
	if reserved != 0 {
		return ChunkTableHeader{}, fmt.Errorf("chunk table reserved field must be 0, got %d: %w", reserved, errdefs.ErrInvalidArgument)
	}
	hashSize, ok := hashAlgoSizes[hashAlgo]
	if !ok {
		return ChunkTableHeader{}, fmt.Errorf("unknown chunk table hash algo %d: %w", hashAlgo, errdefs.ErrInvalidArgument)
	}

	return ChunkTableHeader{
		UncompressedSizeBytes: uncompressedSize,
		ChunkSizeBytes:        chunkSize,
		HashAlgo:              hashAlgo,
		HashSize:              hashSize,
	}, nil
}

/*
Chunk Entry (repeated)
┌───────────────────────┬──────────────┬───────────────────────────────────────────────┐
│ Field                 │ Size (bytes) │ Description                                   │
├───────────────────────┼──────────────┼───────────────────────────────────────────────┤
│ Block Offset          │ 8            │ absolute file offset of chunk zstd            │
│                       │              │ frame start (points at zstd magic)            │
│ Checksum              │ N            │ optional; N = hash size (64 for SHA-512)      │
└───────────────────────┴──────────────┴───────────────────────────────────────────────┘

readChunkEntries reads all chunk table entries and validates monotonicity.
*/
func readChunkEntries(r io.Reader, entryCount int, hashSize uint32) ([]ChunkTableEntry, error) {
	entries := make([]ChunkTableEntry, 0, entryCount)
	var entryBuf [8]byte

	for i := 0; i < entryCount; i++ {
		// Read the entry
		if _, err := io.ReadFull(r, entryBuf[:]); err != nil {
			return nil, fmt.Errorf("failed to read chunk entry %d block offset: %w", i, err)
		}
		blockOffsetU64 := binary.LittleEndian.Uint64(entryBuf[:])
		if blockOffsetU64 > math.MaxInt64 {
			return nil, fmt.Errorf("chunk entry %d block offset %d overflows int64: %w", i, blockOffsetU64, errdefs.ErrOutOfRange)
		}
		blockOffset := int64(blockOffsetU64)

		// read the checksum, if present & validate
		var checksum []byte
		if hashSize != 0 {
			checksum = make([]byte, hashSize)
			if _, err := io.ReadFull(r, checksum); err != nil {
				return nil, fmt.Errorf("failed to read chunk entry %d checksum: %w", i, err)
			}
		}
		entries = append(entries, ChunkTableEntry{
			BlockOffset: blockOffset,
			Checksum:    checksum,
		})
	}

	return entries, nil
}

// writeChunkTable writes a chunk table skippable frame and returns the offset, bytes written,
// and digest of the chunk table payload (formatted as OCI digest "sha512:<hex>").
func writeChunkTable(ctx context.Context, out *countingWriter, tbl ChunkTable) (chunkTableOffset int64, bytesWritten int64, chunkTableDigest string, err error) {
	if err = ctx.Err(); err != nil {
		return 0, 0, "", err
	}
	if err = validateChunkTable(tbl); err != nil {
		return 0, 0, "", err
	}

	chunkTableOffset = out.Offset()
	// payload here means the zstd skippable frame payload: it's chunk header + entries
	chunkTablePayloadSize := uint32(chunkTableHeaderSizeBytes + len(tbl.Entries)*(8+int(tbl.Header.HashSize)))

	// Build the chunk table bytes in a buffer to compute the digest.
	chunkTableWriter := bytes.NewBuffer(make([]byte, 0, chunkTablePayloadSize))
	if err = writeChunkTablePayload(chunkTableWriter, tbl); err != nil {
		return chunkTableOffset, 0, "", err
	}
	chunkTableBytes := chunkTableWriter.Bytes()

	// Compute SHA-512 digest of the chunk table (OCI digest format).
	chunkTableDigest = digest.SHA512.FromBytes(chunkTableBytes).String()

	// Write skippable frame header then chunk table bytes.
	if err = writeSkippableFrameHeader(out, zstdSkippableMagicMin, chunkTablePayloadSize); err != nil {
		return chunkTableOffset, 0, "", err
	}
	if _, err = out.Write(chunkTableBytes); err != nil {
		return chunkTableOffset, 0, "", fmt.Errorf("failed to write chunk table: %w", err)
	}
	return chunkTableOffset, int64(zstdSkippableHeaderSizeBytes) + int64(chunkTablePayloadSize), chunkTableDigest, nil
}

// This is a payload in the sense that it is the payload of the skippable frame.
// Includes both the header and entries of the chunk table.
func writeChunkTablePayload(w io.Writer, tbl ChunkTable) error {
	var hdr [chunkTableHeaderSizeBytes]byte
	binary.LittleEndian.PutUint32(hdr[0:4], chunkTableMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], chunkTableVersion)
	binary.LittleEndian.PutUint64(hdr[8:16], tbl.Header.UncompressedSizeBytes)
	binary.LittleEndian.PutUint32(hdr[16:20], tbl.Header.ChunkSizeBytes)
	hdr[20] = byte(tbl.Header.HashAlgo)
	// hdr[21:23] reserved == 0
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("failed to write chunk table header: %w", err)
	}

	var entryBuf [8]byte
	for i, e := range tbl.Entries {
		// BlockOffset is int64 in memory but stored as uint64 on disk.
		// Negative offsets are rejected by validateChunkTable, so this cast is safe.
		binary.LittleEndian.PutUint64(entryBuf[:], uint64(e.BlockOffset))
		if _, err := w.Write(entryBuf[:]); err != nil {
			return fmt.Errorf("failed to write chunk entry %d BlockOffset: %w", i, err)
		}
		if len(e.Checksum) > 0 {
			if _, err := w.Write(e.Checksum); err != nil {
				return fmt.Errorf("failed to write chunk entry %d checksum: %w", i, err)
			}
		}
	}
	return nil
}

func validateChunkTable(tbl ChunkTable) error {
	if tbl.Header.ChunkSizeBytes == 0 {
		return fmt.Errorf("chunk table chunk size is 0: %w", errdefs.ErrInvalidArgument)
	}
	if _, ok := hashAlgoSizes[tbl.Header.HashAlgo]; !ok {
		return fmt.Errorf("unknown chunk table hash algo %d: %w", tbl.Header.HashAlgo, errdefs.ErrInvalidArgument)
	}
	expectedEntries := numChunks(tbl.Header.UncompressedSizeBytes, tbl.Header.ChunkSizeBytes)
	if len(tbl.Entries) != expectedEntries {
		return fmt.Errorf("chunk table entry count mismatch: got %d, expected %d: %w", len(tbl.Entries), expectedEntries, errdefs.ErrInvalidArgument)
	}
	for i, e := range tbl.Entries {
		if e.BlockOffset < 0 {
			return fmt.Errorf("chunk %d has negative block offset %d: %w", i, e.BlockOffset, errdefs.ErrOutOfRange)
		}
		if int(tbl.Header.HashSize) != len(e.Checksum) {
			return fmt.Errorf("chunk %d checksum size mismatch: got %d, expected %d: %w", i, len(e.Checksum), tbl.Header.HashSize, errdefs.ErrInvalidArgument)
		}
		if i > 0 && tbl.Entries[i-1].BlockOffset > e.BlockOffset {
			return fmt.Errorf("chunk offsets not monotonic at %d: %d > %d: %w", i-1, tbl.Entries[i-1].BlockOffset, e.BlockOffset, errdefs.ErrOutOfRange)
		}
	}
	return nil
}

func numChunks(uncompressedSize uint64, chunkSize uint32) int {
	if uncompressedSize == 0 {
		return 0
	}
	chunkSizeU64 := uint64(chunkSize)
	// round up to account for a possible last partial chunk
	return int((uncompressedSize + chunkSizeU64 - 1) / chunkSizeU64)
}

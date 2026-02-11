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
	"strings"
	"testing"
)

// testChunkTableConfig holds parameters for creating test chunk tables.
type testChunkTableConfig struct {
	numChunks int
	chunkSize uint32
	hashAlgo  uint32
	// If true, the last chunk is partial (uncompressedSize < numChunks * chunkSize).
	partialLastChunk bool
}

// makeTestChunkTable creates a ChunkTable for testing with realistic values.
//
// Block offsets simulate compressed frame positions:
//   - Each frame is assumed to compress to ~80% of chunk size for simplicity
//   - Offsets are monotonically increasing
//
// Checksums are filled with incrementing byte patterns (0x01, 0x02, etc.)
// for easy identification in test failures.
func makeTestChunkTable(cfg testChunkTableConfig) ChunkTable {
	hashSize := hashAlgoSizes[cfg.hashAlgo]

	// Calculate uncompressed size. For a partial last chunk, subtract 1 byte.
	uncompressedSize := uint64(cfg.numChunks) * uint64(cfg.chunkSize)
	if cfg.partialLastChunk && cfg.numChunks > 0 {
		uncompressedSize--
	}

	// Build entries with realistic block offsets.
	// Simulate ~80% compression ratio for each chunk.
	compressedFrameSize := int64(float64(cfg.chunkSize) * 0.8)
	if compressedFrameSize < 1 {
		compressedFrameSize = 1
	}

	entries := make([]ChunkTableEntry, cfg.numChunks)
	for i := range entries {
		entries[i] = ChunkTableEntry{
			BlockOffset: int64(i) * compressedFrameSize,
		}
		if hashSize > 0 {
			// Fill checksum with a repeating byte pattern for easy identification.
			entries[i].Checksum = bytes.Repeat([]byte{byte(i + 1)}, int(hashSize))
		}
	}

	return ChunkTable{
		Header: ChunkTableHeader{
			UncompressedSizeBytes: uncompressedSize,
			ChunkSizeBytes:        cfg.chunkSize,
			HashAlgo:              cfg.hashAlgo,
			HashSize:              hashSize,
		},
		Entries: entries,
	}
}

// endOfChunksOffset calculates where the compressed chunk data would end,
// based on the table's entries and a simulated compression ratio.
func endOfChunksOffset(tbl ChunkTable) int64 {
	if len(tbl.Entries) == 0 {
		return 0
	}
	lastEntry := tbl.Entries[len(tbl.Entries)-1]
	// Assume the last frame has similar size to inter-frame gaps.
	if len(tbl.Entries) > 1 {
		avgFrameSize := lastEntry.BlockOffset / int64(len(tbl.Entries)-1)
		return lastEntry.BlockOffset + avgFrameSize
	}
	// Single entry: assume some compressed size.
	return lastEntry.BlockOffset + int64(float64(tbl.Header.ChunkSizeBytes)*0.8)
}

// TestChunkTable_RoundTrip verifies that chunk tables serialize and deserialize correctly
// across different hash algorithms and partial/full last chunk scenarios.
func TestChunkTable_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		numChunks        int
		hashAlgo         uint32
		partialLastChunk bool
	}{
		// No checksum cases
		{"no_checksum/partial_last_chunk", 3, chunkHashAlgoNone, true},
		{"no_checksum/full_last_chunk", 3, chunkHashAlgoNone, false},
		{"no_checksum/single_chunk_partial", 1, chunkHashAlgoNone, true},
		{"no_checksum/single_chunk_full", 1, chunkHashAlgoNone, false},

		// SHA-512 checksum cases
		{"sha512/partial_last_chunk", 3, chunkHashAlgoSHA512, true},
		{"sha512/full_last_chunk", 3, chunkHashAlgoSHA512, false},
		{"sha512/single_chunk_partial", 1, chunkHashAlgoSHA512, true},
		{"sha512/single_chunk_full", 1, chunkHashAlgoSHA512, false},
		{"sha512/many_chunks", 5, chunkHashAlgoSHA512, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// Create chunk table with small chunk size (1KB) for fast tests.
			tbl := makeTestChunkTable(testChunkTableConfig{
				numChunks:        tt.numChunks,
				chunkSize:        1024,
				hashAlgo:         tt.hashAlgo,
				partialLastChunk: tt.partialLastChunk,
			})

			// Write chunk table to buffer.
			var buf bytes.Buffer
			cw := &countingWriter{w: &buf}
			blobOffset, bytesWritten, chunkTableDigest, err := writeChunkTable(ctx, cw, tbl)
			if err != nil {
				t.Fatalf("writeChunkTable: %v", err)
			}
			if blobOffset != 0 {
				t.Fatalf("expected blob offset 0, got %d", blobOffset)
			}
			if bytesWritten != int64(buf.Len()) {
				t.Fatalf("bytesWritten mismatch: got %d, want %d", bytesWritten, buf.Len())
			}
			if chunkTableDigest == "" || !strings.HasPrefix(chunkTableDigest, "sha512:") {
				t.Fatalf("expected sha512 digest, got %q", chunkTableDigest)
			}

			// Read chunk table back with digest validation.
			readerAt := bytes.NewReader(buf.Bytes())
			got, err := ReadChunkTable(ctx, readerAt, 0, chunkTableDigest)
			if err != nil {
				t.Fatalf("ReadChunkTable: %v", err)
			}

			// Verify header fields.
			if got.Header.UncompressedSizeBytes != tbl.Header.UncompressedSizeBytes {
				t.Fatalf("UncompressedSizeBytes: got %d, want %d", got.Header.UncompressedSizeBytes, tbl.Header.UncompressedSizeBytes)
			}
			if got.Header.ChunkSizeBytes != tbl.Header.ChunkSizeBytes {
				t.Fatalf("ChunkSizeBytes: got %d, want %d", got.Header.ChunkSizeBytes, tbl.Header.ChunkSizeBytes)
			}
			if got.Header.HashAlgo != tbl.Header.HashAlgo {
				t.Fatalf("HashAlgo: got %d, want %d", got.Header.HashAlgo, tbl.Header.HashAlgo)
			}
			if got.Header.HashSize != tbl.Header.HashSize {
				t.Fatalf("HashSize: got %d, want %d", got.Header.HashSize, tbl.Header.HashSize)
			}

			// Verify entries.
			if len(got.Entries) != len(tbl.Entries) {
				t.Fatalf("Entries count: got %d, want %d", len(got.Entries), len(tbl.Entries))
			}
			for i := range tbl.Entries {
				if got.Entries[i].BlockOffset != tbl.Entries[i].BlockOffset {
					t.Fatalf("Entries[%d].BlockOffset: got %d, want %d", i, got.Entries[i].BlockOffset, tbl.Entries[i].BlockOffset)
				}
				expectedChecksumLen := int(tbl.Header.HashSize)
				if len(got.Entries[i].Checksum) != expectedChecksumLen {
					t.Fatalf("Entries[%d].Checksum: got %d bytes, want %d", i, len(got.Entries[i].Checksum), expectedChecksumLen)
				}
				if !bytes.Equal(got.Entries[i].Checksum, tbl.Entries[i].Checksum) {
					t.Fatalf("Entries[%d].Checksum mismatch", i)
				}
			}

			// Verify FrameBounds returns correct checksums.
			endOfChunks := endOfChunksOffset(tbl)
			for i := 0; i < len(tbl.Entries); i++ {
				_, _, checksum, err := got.FrameBounds(i, endOfChunks)
				if err != nil {
					t.Fatalf("FrameBounds(%d): %v", i, err)
				}
				if !bytes.Equal(checksum, tbl.Entries[i].Checksum) {
					t.Fatalf("FrameBounds(%d) checksum mismatch", i)
				}
			}
		})
	}
}

func TestChunkTable_FrameBounds(t *testing.T) {
	t.Parallel()

	t.Log("create chunk table with 3 entries for frame bounds testing")
	tbl := makeTestChunkTable(testChunkTableConfig{
		numChunks:        3,
		chunkSize:        1024, // 1KB chunks
		hashAlgo:         chunkHashAlgoNone,
		partialLastChunk: true,
	})
	// With 1024-byte chunks and 80% compression, offsets are: 0, 819, 1638
	// endOfChunks would be around 2457
	endOfChunks := endOfChunksOffset(tbl)

	t.Log("verify first chunk bounds [0, 819)")
	start, end, checksum, err := tbl.FrameBounds(0, endOfChunks)
	if err != nil {
		t.Fatalf("FrameBounds(0): %v", err)
	}
	if start != tbl.Entries[0].BlockOffset {
		t.Fatalf("FrameBounds(0): start got %d, want %d", start, tbl.Entries[0].BlockOffset)
	}
	if end != tbl.Entries[1].BlockOffset {
		t.Fatalf("FrameBounds(0): end got %d, want %d", end, tbl.Entries[1].BlockOffset)
	}
	if len(checksum) != 0 {
		t.Fatalf("FrameBounds(0): expected empty checksum, got %d bytes", len(checksum))
	}

	t.Log("verify middle chunk bounds [819, 1638)")
	start, end, checksum, err = tbl.FrameBounds(1, endOfChunks)
	if err != nil {
		t.Fatalf("FrameBounds(1): %v", err)
	}
	if start != tbl.Entries[1].BlockOffset {
		t.Fatalf("FrameBounds(1): start got %d, want %d", start, tbl.Entries[1].BlockOffset)
	}
	if end != tbl.Entries[2].BlockOffset {
		t.Fatalf("FrameBounds(1): end got %d, want %d", end, tbl.Entries[2].BlockOffset)
	}
	if len(checksum) != 0 {
		t.Fatalf("FrameBounds(1): expected empty checksum, got %d bytes", len(checksum))
	}

	t.Log("verify last chunk bounds [1638, endOfChunks)")
	start, end, checksum, err = tbl.FrameBounds(2, endOfChunks)
	if err != nil {
		t.Fatalf("FrameBounds(2): %v", err)
	}
	if start != tbl.Entries[2].BlockOffset {
		t.Fatalf("FrameBounds(2): start got %d, want %d", start, tbl.Entries[2].BlockOffset)
	}
	if end != endOfChunks {
		t.Fatalf("FrameBounds(2): end got %d, want %d", end, endOfChunks)
	}
	if len(checksum) != 0 {
		t.Fatalf("FrameBounds(2): expected empty checksum, got %d bytes", len(checksum))
	}
}

func TestReadChunkTable_DigestValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Log("write a chunk table to get a valid digest")
	tbl := makeTestChunkTable(testChunkTableConfig{
		numChunks:        1,
		chunkSize:        1024, // 1KB chunks
		hashAlgo:         chunkHashAlgoNone,
		partialLastChunk: true,
	})

	var buf bytes.Buffer
	// countingWriter is required because writeChunkTable takes *countingWriter
	// to record where the chunk table starts in the output stream.
	cw := &countingWriter{w: &buf}
	_, _, validDigest, err := writeChunkTable(ctx, cw, tbl)
	if err != nil {
		t.Fatalf("writeChunkTable: %v", err)
	}

	readerAt := bytes.NewReader(buf.Bytes())

	tests := []struct {
		name    string
		digest  string
		wantErr bool
	}{
		{"valid digest", validDigest, false},
		{"empty digest skips validation", "", false},
		{"wrong digest", "sha512:" + strings.Repeat("00", 64), true},
		{"invalid format", "not-a-valid-digest", true},
		{"unsupported algorithm", "sha256:" + strings.Repeat("00", 32), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadChunkTable(ctx, readerAt, 0, tt.digest)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadChunkTable() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

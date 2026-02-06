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
	"encoding/binary"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// ===========================================================================
// Shared test data generators and helpers
// ===========================================================================

// Common chunk sizes used across tests.
const (
	chunkSize1MiB  = 1 << 20         // 1 MiB - standard chunk size for most tests
	chunkSize64KiB = 64 * 1024       // 64 KiB - smaller size for faster tests
	chunkSize4MiB  = 4 * 1024 * 1024 // 4 MiB - production default
)

// testDataSize represents common test data sizes.
type testDataSize int

const (
	// testDataTiny is smaller than any chunk size (50 bytes).
	testDataTiny testDataSize = 50
	// testDataSmall is a few hundred bytes for quick tests.
	testDataSmall testDataSize = 256
	// testDataMedium is ~512 KiB for moderate tests.
	testDataMedium testDataSize = 512 * 1024
	// testDataLarge is ~2 MiB for multi-frame round-trip tests.
	testDataLarge testDataSize = 2 * 1024 * 1024
)

// generateTestData creates test data of the specified size using a repeating pattern.
// The pattern is designed to be somewhat compressible but not trivially so.
func generateTestData(size testDataSize) []byte {
	pattern := []byte("containerd-seekable-erofs-test-")
	return bytes.Repeat(pattern, (int(size)+len(pattern)-1)/len(pattern))[:size]
}

// generateTestDataExact creates test data of exactly the specified byte count.
// Useful for exact boundary tests.
func generateTestDataExact(sizeBytes int) []byte {
	pattern := []byte("X")
	return bytes.Repeat(pattern, sizeBytes)
}

// encodedTestBlob holds the result of encoding test data.
type encodedTestBlob struct {
	Plain        []byte
	Blob         []byte
	EncodeResult EncodeResult
}

// encodeTestData encodes plain data into a seekable EROFS blob.
// This is the primary helper for tests that need to encode and then verify.
func encodeTestData(t *testing.T, plain []byte, chunkSize int, includeDMVerity bool) encodedTestBlob {
	t.Helper()
	var blob bytes.Buffer
	opts := EncodeOptions{
		ChunkSizeBytes:  chunkSize,
		IncludeDMVerity: includeDMVerity,
	}
	if includeDMVerity {
		opts.DMVerityBlockSize = DefaultDMVerityBlockSize
	}
	result, err := EncodeErofsImageTo(context.Background(), bytes.NewReader(plain), &blob, opts)
	if err != nil {
		t.Fatalf("EncodeErofsImageTo: %v", err)
	}
	return encodedTestBlob{
		Plain:        plain,
		Blob:         blob.Bytes(),
		EncodeResult: result,
	}
}

// verifyDecodeAll decodes the blob using DecodeErofsAll and verifies it matches the original.
func verifyDecodeAll(t *testing.T, blob, expectedPlain []byte) {
	t.Helper()
	var decoded bytes.Buffer
	n, err := DecodeErofsAll(context.Background(), bytes.NewReader(blob), &decoded)
	if err != nil {
		t.Fatalf("DecodeErofsAll: %v", err)
	}
	if n != int64(len(expectedPlain)) {
		t.Fatalf("DecodeErofsAll: expected %d bytes, got %d", len(expectedPlain), n)
	}
	if !bytes.Equal(decoded.Bytes(), expectedPlain) {
		t.Fatalf("DecodeErofsAll: output mismatch (len: %d vs %d)", decoded.Len(), len(expectedPlain))
	}
}

// verifyChunkTable reads and validates the chunk table from an encoded blob.
func verifyChunkTable(t *testing.T, blob []byte, result EncodeResult, expectedChunks int) *ChunkTable {
	t.Helper()
	blobReader := bytes.NewReader(blob)
	tbl, err := ReadChunkTable(context.Background(), blobReader, result.ChunkTableOffset, result.ChunkTableDigest)
	if err != nil {
		t.Fatalf("ReadChunkTable: %v", err)
	}
	if len(tbl.Entries) != expectedChunks {
		t.Fatalf("chunk table entries: got %d, want %d", len(tbl.Entries), expectedChunks)
	}
	return tbl
}

// verifyPerFrameDecode decodes each frame individually and verifies the reconstructed output.
func verifyPerFrameDecode(t *testing.T, blob []byte, tbl *ChunkTable, endOfChunks int64, expectedPlain []byte) {
	t.Helper()
	blobReader := bytes.NewReader(blob)
	var reconstructed bytes.Buffer

	for i := range tbl.Entries {
		frameStart, frameEnd, checksum, err := tbl.FrameBounds(i, endOfChunks)
		if err != nil {
			t.Fatalf("FrameBounds(%d): %v", i, err)
		}
		var frameOut bytes.Buffer
		_, err = DecodeErofsFrame(context.Background(), blobReader, frameStart, frameEnd, checksum, &frameOut)
		if err != nil {
			t.Fatalf("DecodeErofsFrame(%d): %v", i, err)
		}
		reconstructed.Write(frameOut.Bytes())
	}

	if !bytes.Equal(reconstructed.Bytes(), expectedPlain) {
		t.Fatalf("per-frame decode mismatch (len: %d vs %d)", reconstructed.Len(), len(expectedPlain))
	}
}

// ===========================================================================
// Tests
// ===========================================================================

func TestDecodeErofsAll_ConcatenatedFramesAndSkippable(t *testing.T) {
	t.Parallel()

	t.Log("create large test data and compress as 1 MiB chunks")
	original := generateTestData(testDataLarge)

	var blob bytes.Buffer
	for off := 0; off < len(original); off += chunkSize1MiB {
		end := off + chunkSize1MiB
		if end > len(original) {
			end = len(original)
		}
		frame, err := encodeOneZstdFrame(original[off:end])
		if err != nil {
			t.Fatalf("encode frame: %v", err)
		}
		blob.Write(frame)
	}

	t.Log("append skippable frames to simulate chunk table and dm-verity")
	if err := writeZstdSkippableFrame(&blob, []byte("chunk table payload")); err != nil {
		t.Fatalf("write skippable frame: %v", err)
	}
	if err := writeZstdSkippableFrame(&blob, []byte("dm-verity payload")); err != nil {
		t.Fatalf("write skippable frame: %v", err)
	}

	t.Log("decode entire blob and verify output matches original")
	verifyDecodeAll(t, blob.Bytes(), original)
}

func TestDecodeErofsFrame_VerifyChecksum(t *testing.T) {
	t.Parallel()

	t.Log("compress small test data and compute SHA-512 checksum")
	plain := generateTestData(testDataSmall)
	frame, err := encodeOneZstdFrame(plain)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}

	t.Log("decode frame with checksum verification")
	checksum := sha512.Sum512(frame)
	var out bytes.Buffer
	n, err := DecodeErofsFrame(context.Background(), bytes.NewReader(frame), 0, int64(len(frame)), checksum[:], &out)
	if err != nil {
		t.Fatalf("DecodeErofsFrame: %v", err)
	}
	if n != int64(len(plain)) {
		t.Fatalf("expected %d bytes, got %d", len(plain), n)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatalf("output mismatch")
	}
}

func TestDecodeErofsFrame_BadChecksumFails(t *testing.T) {
	t.Parallel()

	plain := generateTestData(testDataSmall)
	frame, err := encodeOneZstdFrame(plain)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}

	bad := make([]byte, sha512.Size)
	bad[0] = 1 // wrong checksum

	var out bytes.Buffer
	_, err = DecodeErofsFrame(context.Background(), bytes.NewReader(frame), 0, int64(len(frame)), bad, &out)
	if err == nil {
		t.Fatalf("expected error for bad checksum, got nil")
	}
}

func TestDecodeErofsFrame_InvalidBounds(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	_, err := DecodeErofsFrame(context.Background(), bytes.NewReader([]byte("x")), 10, 5, nil, &out)
	if err == nil {
		t.Fatalf("expected error for invalid bounds (start > end), got nil")
	}
}

func TestEncodeErofsImageTo_RoundTripViaDecodeAll(t *testing.T) {
	t.Parallel()

	t.Log("encode large test data as seekable EROFS blob")
	plain := generateTestData(testDataLarge)
	encoded := encodeTestData(t, plain, chunkSize1MiB, false)

	t.Log("verify encode result metadata")
	expectedFrames := (len(plain) + chunkSize1MiB - 1) / chunkSize1MiB
	tbl := verifyChunkTable(t, encoded.Blob, encoded.EncodeResult, expectedFrames)
	if tbl.Header.UncompressedSizeBytes != uint64(len(plain)) {
		t.Fatalf("expected UncompressedSizeBytes=%d, got %d", len(plain), tbl.Header.UncompressedSizeBytes)
	}
	if tbl.Header.ChunkSizeBytes != uint32(chunkSize1MiB) {
		t.Fatalf("expected ChunkSizeBytes=%d, got %d", chunkSize1MiB, tbl.Header.ChunkSizeBytes)
	}

	t.Log("decode blob and verify round-trip")
	verifyDecodeAll(t, encoded.Blob, plain)
}

func TestEncodeErofsImageTo_WithDMVerity_RoundTripViaDecodeAll(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("dm-verity encoding is only supported on linux")
	}

	t.Log("encode medium test data with dm-verity enabled")
	plain := generateTestData(testDataMedium)
	encoded := encodeTestData(t, plain, chunkSize1MiB, true)

	t.Log("verify dm-verity metadata")
	if !encoded.EncodeResult.HasDMVerity {
		t.Fatalf("expected HasDMVerity=true, got false")
	}
	if !strings.HasPrefix(encoded.EncodeResult.DMVerityRootDigest, "sha512:") {
		t.Fatalf("expected DMVerityRootDigest sha512:..., got %q", encoded.EncodeResult.DMVerityRootDigest)
	}

	t.Log("verify dm-verity skippable frame structure")
	if encoded.EncodeResult.DMVerityOffset+8 > int64(len(encoded.Blob)) {
		t.Fatalf("DMVerityOffset out of bounds: %d (len=%d)", encoded.EncodeResult.DMVerityOffset, len(encoded.Blob))
	}
	magic := binary.LittleEndian.Uint32(encoded.Blob[encoded.EncodeResult.DMVerityOffset : encoded.EncodeResult.DMVerityOffset+4])
	if magic != zstdSkippableMagicMin {
		t.Fatalf("expected skippable magic 0x%08x, got 0x%08x", zstdSkippableMagicMin, magic)
	}
	// Payload begins with "verity\0\0" signature.
	payloadStart := encoded.EncodeResult.DMVerityOffset + 8
	if string(encoded.Blob[payloadStart:payloadStart+8]) != "verity\x00\x00" {
		t.Fatalf("expected dm-verity signature at payload start")
	}

	t.Log("decode blob and verify round-trip")
	verifyDecodeAll(t, encoded.Blob, plain)
}

// TestEncodeDecodeViaChunkTable_FullPath exercises the complete single-frame read path:
// encode → read chunk table → decode each frame individually → compare to original
func TestEncodeDecodeViaChunkTable_FullPath(t *testing.T) {
	t.Parallel()

	t.Log("encode large test data as seekable EROFS blob")
	plain := generateTestData(testDataLarge)
	encoded := encodeTestData(t, plain, chunkSize1MiB, false)

	t.Log("read and verify chunk table")
	expectedChunks := (len(plain) + chunkSize1MiB - 1) / chunkSize1MiB
	tbl := verifyChunkTable(t, encoded.Blob, encoded.EncodeResult, expectedChunks)

	t.Log("verify chunk table metadata")
	if tbl.Header.UncompressedSizeBytes != uint64(len(plain)) {
		t.Fatalf("chunk table uncompressed size: got %d, want %d", tbl.Header.UncompressedSizeBytes, len(plain))
	}
	if int(tbl.Header.ChunkSizeBytes) != chunkSize1MiB {
		t.Fatalf("chunk table chunk size: got %d, want %d", tbl.Header.ChunkSizeBytes, chunkSize1MiB)
	}

	t.Log("decode each frame individually and verify")
	verifyPerFrameDecode(t, encoded.Blob, tbl, encoded.EncodeResult.ChunkTableOffset, plain)

	t.Log("verify per-frame decode matches stream decode")
	verifyDecodeAll(t, encoded.Blob, plain)
}

// TestEncodeErofsImageTo_TinyImage tests an image smaller than one chunk
func TestEncodeErofsImageTo_TinyImage(t *testing.T) {
	t.Parallel()

	t.Log("encode tiny test data (smaller than chunk size)")
	plain := generateTestData(testDataTiny)
	encoded := encodeTestData(t, plain, chunkSize1MiB, false)

	t.Log("verify single frame produced")
	tbl := verifyChunkTable(t, encoded.Blob, encoded.EncodeResult, 1)
	if tbl.Header.UncompressedSizeBytes != uint64(len(plain)) {
		t.Fatalf("unexpected uncompressed size: got %d, want %d", tbl.Header.UncompressedSizeBytes, len(plain))
	}

	t.Log("verify round-trip")
	verifyDecodeAll(t, encoded.Blob, plain)
}

// TestEncodeErofsImageTo_ExactChunkBoundary tests an image exactly divisible by chunk size
func TestEncodeErofsImageTo_ExactChunkBoundary(t *testing.T) {
	t.Parallel()

	numChunks := 4
	plainSize := chunkSize64KiB * numChunks

	t.Log("encode data that is exactly divisible by chunk size")
	plain := generateTestDataExact(plainSize)
	encoded := encodeTestData(t, plain, chunkSize64KiB, false)

	t.Log("verify exact number of frames produced")
	tbl := verifyChunkTable(t, encoded.Blob, encoded.EncodeResult, numChunks)
	if tbl.Header.UncompressedSizeBytes != uint64(plainSize) {
		t.Fatalf("unexpected uncompressed size: got %d, want %d", tbl.Header.UncompressedSizeBytes, plainSize)
	}

	t.Log("verify round-trip")
	verifyDecodeAll(t, encoded.Blob, plain)

	t.Log("verify each chunk decodes to exactly chunkSize bytes")
	blobReader := bytes.NewReader(encoded.Blob)
	endOfChunks := encoded.EncodeResult.ChunkTableOffset
	for i := range tbl.Entries {
		frameStart, frameEnd, checksum, err := tbl.FrameBounds(i, endOfChunks)
		if err != nil {
			t.Fatalf("FrameBounds(%d): %v", i, err)
		}
		var frameOut bytes.Buffer
		n, err := DecodeErofsFrame(context.Background(), blobReader, frameStart, frameEnd, checksum, &frameOut)
		if err != nil {
			t.Fatalf("DecodeErofsFrame(%d): %v", i, err)
		}
		if n != int64(chunkSize64KiB) {
			t.Fatalf("frame %d: expected %d bytes, got %d", i, chunkSize64KiB, n)
		}
	}
}

// TestEncodeErofsImageTo_PartialLastChunk verifies a partial final chunk when multiple chunks exist.
func TestEncodeErofsImageTo_PartialLastChunk(t *testing.T) {
	t.Parallel()

	chunkSize := chunkSize64KiB
	remainder := 123
	plainSize := chunkSize*2 + remainder

	t.Log("encode data with a partial final chunk")
	plain := generateTestDataExact(plainSize)
	encoded := encodeTestData(t, plain, chunkSize, false)

	t.Log("verify expected number of chunks and round-trip")
	tbl := verifyChunkTable(t, encoded.Blob, encoded.EncodeResult, 3)
	verifyDecodeAll(t, encoded.Blob, plain)

	t.Log("verify last chunk decodes to remainder size")
	blobReader := bytes.NewReader(encoded.Blob)
	endOfChunks := encoded.EncodeResult.ChunkTableOffset
	for i := range tbl.Entries {
		frameStart, frameEnd, checksum, err := tbl.FrameBounds(i, endOfChunks)
		if err != nil {
			t.Fatalf("FrameBounds(%d): %v", i, err)
		}
		var frameOut bytes.Buffer
		n, err := DecodeErofsFrame(context.Background(), blobReader, frameStart, frameEnd, checksum, &frameOut)
		if err != nil {
			t.Fatalf("DecodeErofsFrame(%d): %v", i, err)
		}
		expectedSize := chunkSize
		if i == len(tbl.Entries)-1 {
			expectedSize = remainder
		}
		if n != int64(expectedSize) {
			t.Fatalf("frame %d: expected %d bytes, got %d", i, expectedSize, n)
		}
	}
}

// ===========================================================================
// Test-only zstd helpers
// ===========================================================================

// encodeOneZstdFrame compresses plain data into a single zstd frame.
//
// Note: The production code has writeZstdFrame() in image.go which is similar,
// but it writes to an io.Writer and computes SHA-512 checksum (for the chunk table).
// This test helper returns bytes directly, which is simpler for tests that just
// need to create compressed data without checksum overhead.
func encodeOneZstdFrame(plain []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	if _, err := enc.Write(plain); err != nil {
		enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeZstdSkippableFrame writes a complete zstd skippable frame (header + payload).
//
// Note: The production code has writeSkippableFrameHeader() in zstd.go which only
// writes the 8-byte header. This test helper writes header + payload together,
// which is more convenient for tests that simulate complete skippable frames.
func writeZstdSkippableFrame(w io.Writer, payload []byte) error {
	if err := writeSkippableFrameHeader(w, zstdSkippableMagicMin, uint32(len(payload))); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

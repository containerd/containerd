//go:build linux

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
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/containerd/containerd/v2/pkg/atomicfile"
	"github.com/containerd/errdefs"
	"github.com/containerd/go-dmverity/pkg/verity"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
)

const (
	// dmVerityHashName is the hash algorithm used for dm-verity data
	dmVerityHashName = "sha512"
)

func IsValidDMVerityBlockSize(size int) bool {
	return verity.IsBlockSizeValid(uint32(size))
}

/*
DM-Verity Payload (inside skippable frame payload)
┌──────────────────────────────────────────────────────────────────────────────┐
│ Superblock (512 bytes)                                                       │
│  - Starts with ASCII "verity" signature                                      │
├──────────────────────────────────────────────────────────────────────────────┤
│ Padding (3584 bytes)                                                         │
│  - Zero-filled padding so superblock region occupies 4096 bytes total        │
├──────────────────────────────────────────────────────────────────────────────┤
│ Merkle Tree (variable length)                                                │
│  - Hash tree computed over (possibly padded) uncompressed EROFS bytes        │
└──────────────────────────────────────────────────────────────────────────────┘
*/

// DMVerityMetadata is written alongside `layer.erofs` and consumed by mount-time setup.
// This matches the sidecar format from PR #12502. All other dm-verity parameters
// (block sizes, salt, algorithm, etc.) are stored in the superblock within the layer blob
// and are auto-detected by the mount-time code via verity.ReadSuperblock().
type DMVerityMetadata struct {
	// RootDigest is the dm-verity root digest, serialized as "<algo>:<hex>" (e.g. "sha512:<hex>").
	RootDigest string `json:"roothash"`

	// HashOffset is the absolute byte offset of the dm-verity superblock within `layer.erofs`.
	// The superblock starts with ASCII "verity" and is always present.
	HashOffset uint64 `json:"hashoffset"`
}

// MaterializeDMVerity is the entry point that the differ should call.
//
// It extracts the dm-verity payload from the blob, appends it to `layer.erofs`,
// and writes a DMVerityMetadata file alongside `layer.erofs`.
//
// dmVerityFrameOffsetInBlob corresponds to the OCI annotation `dmverity.offset` and points to the
// start of the dm-verity skippable frame header.
//
// The dm-verity block size is read from the superblock within the payload, so it does not
// need to be passed as a parameter.
func MaterializeDMVerity(
	ctx context.Context,
	blob io.ReaderAt,
	blobSize int64, // Total size of the compressed blob (used for bounds checking)
	dmVerityFrameOffsetInBlob int64,
	layerPath string,
	rootDigest string,
) (DMVerityMetadata, error) {
	if err := validateMaterializeDMVerityArgs(ctx, blob, blobSize, dmVerityFrameOffsetInBlob, layerPath, rootDigest); err != nil {
		return DMVerityMetadata{}, err
	}

	// Read dm-verity payload from the blob.
	payload, payloadSize, err := readDMVerityPayload(blob, blobSize, dmVerityFrameOffsetInBlob)
	if err != nil {
		return DMVerityMetadata{}, fmt.Errorf("failed to read dm-verity payload: %w", err)
	}

	// Read the superblock to get the block size for padding calculations.
	// The superblock is at offset 0 within the payload.
	superblock, err := verity.ReadSuperblock(payload, 0)
	if err != nil {
		return DMVerityMetadata{}, fmt.Errorf("failed to read dm-verity superblock: %w", err)
	}
	blockSize := int(superblock.DataBlockSize)

	// Rewind the payload reader for copying.
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return DMVerityMetadata{}, fmt.Errorf("failed to rewind dm-verity payload: %w", err)
	}

	// Append payload to the layer file.
	dmVerityOffsetInLayer, err := appendPayloadToLayerFile(ctx, layerPath, payload, payloadSize, blockSize)
	if err != nil {
		return DMVerityMetadata{}, err
	}

	// Build and write metadata file.
	meta := DMVerityMetadata{
		RootDigest: rootDigest,
		HashOffset: uint64(dmVerityOffsetInLayer),
	}
	if err := writeDMVerityMetadata(ctx, layerPath, meta); err != nil {
		return DMVerityMetadata{}, fmt.Errorf("failed to write dm-verity metadata: %w", err)
	}
	return meta, nil
}

// ReadDMVerityMetadata reads DMVerityMetadata written alongside `layer.erofs`.
func ReadDMVerityMetadata(layerPath string) (DMVerityMetadata, error) {
	if layerPath == "" {
		return DMVerityMetadata{}, fmt.Errorf("layer path is empty: %w", errdefs.ErrInvalidArgument)
	}

	metaPath := dmVerityMetadataPath(layerPath)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return DMVerityMetadata{}, err
	}
	var m DMVerityMetadata
	if err := json.Unmarshal(b, &m); err != nil {
		return DMVerityMetadata{}, fmt.Errorf("failed to parse dm-verity metadata: %w", err)
	}
	return m, nil
}

// WriteDMVeritySkippableFrameAtEOF appends a dm-verity payload as a zstd skippable frame at EOF.
//
// Payload format (superblock-present mode):
//   - superblock at offset 0 (512 bytes)
//   - zero padding until block size bytes
//   - hash tree blocks starting at hashAreaOffset (equal to blockSizeBytes)
//
// Returns dmverity.offset (skippable frame header start), root digest ("<algo>:<hex>"), and bytes written.
func WriteDMVeritySkippableFrameAtEOF(ctx context.Context, out *countingWriter, erofsDataPath string, erofsSizeBytes int64, blockSizeBytes int) (dmVerityFrameOffsetInBlob int64, rootDigest string, bytesWritten int64, err error) {
	if err := validateWriteDMVerityArgs(ctx, out, erofsDataPath, erofsSizeBytes, blockSizeBytes); err != nil {
		return 0, "", 0, err
	}

	blockSize := uint32(blockSizeBytes)

	// Pad data file to block alignment and get final size.
	paddedSize, err := prepareDataFileForVerity(ctx, erofsDataPath, erofsSizeBytes, blockSize)
	if err != nil {
		return 0, "", 0, err
	}

	// Create the verity hash tree in a temp file.
	hashPath, rootDigest, err := createVerityHashTree(erofsDataPath, paddedSize, blockSize)
	if err != nil {
		return 0, "", 0, err
	}
	defer func() { _ = os.Remove(hashPath) }()

	// Write hash tree as a skippable frame.
	dmHeaderOffset, bytesWritten, err := writeHashTreeAsSkippableFrame(out, hashPath)
	if err != nil {
		return 0, "", 0, err
	}

	return dmHeaderOffset, rootDigest, bytesWritten, nil
}

func validateWriteDMVerityArgs(ctx context.Context, out *countingWriter, erofsDataPath string, erofsSizeBytes int64, blockSizeBytes int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if out == nil {
		return fmt.Errorf("dm-verity output writer is nil: %w", errdefs.ErrInvalidArgument)
	}
	if erofsDataPath == "" {
		return fmt.Errorf("erofs data path is empty: %w", errdefs.ErrInvalidArgument)
	}
	if erofsSizeBytes < 0 {
		return fmt.Errorf("invalid erofs size %d (must be >= 0): %w", erofsSizeBytes, errdefs.ErrInvalidArgument)
	}
	if blockSizeBytes <= 0 {
		return fmt.Errorf("invalid dm-verity block size %d (must be > 0): %w", blockSizeBytes, errdefs.ErrInvalidArgument)
	}
	if !verity.IsBlockSizeValid(uint32(blockSizeBytes)) {
		return fmt.Errorf("invalid dm-verity block size %d: %w", blockSizeBytes, errdefs.ErrInvalidArgument)
	}
	return nil
}

// prepareDataFileForVerity opens the data file, validates its size, pads it to block alignment,
// and returns the final padded size.
func prepareDataFileForVerity(ctx context.Context, dataPath string, expectedSize int64, blockSize uint32) (paddedSize int64, err error) {
	dataFile, err := os.OpenFile(dataPath, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to open erofs data file: %w", err)
	}
	defer dataFile.Close()

	dataStat, err := dataFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat erofs data file: %w", err)
	}

	actualSize := dataStat.Size()
	if expectedSize != 0 && actualSize != expectedSize {
		return 0, fmt.Errorf("erofs data file size mismatch: expected %d bytes, got %d bytes: %w", expectedSize, actualSize, errdefs.ErrFailedPrecondition)
	}
	if expectedSize == 0 {
		expectedSize = actualSize
	}

	if _, err := dataFile.Seek(0, io.SeekEnd); err != nil {
		return 0, fmt.Errorf("failed to seek erofs data file for padding: %w", err)
	}

	padded, err := writeBlockPadding(dataFile, expectedSize, int(blockSize))
	if err != nil {
		return 0, fmt.Errorf("failed to pad erofs data for dm-verity: %w", err)
	}

	return expectedSize + padded, nil
}

// createVerityHashTree creates a dm-verity hash tree for the data file.
// Returns the path to the temp file containing the hash tree and the root digest.
func createVerityHashTree(dataPath string, dataSizeBytes int64, blockSize uint32) (hashPath string, rootDigest string, err error) {
	dataBlocks := uint64(dataSizeBytes / int64(blockSize))

	hashTmp, err := os.CreateTemp("", "dmverity-hash-*.bin")
	if err != nil {
		return "", "", fmt.Errorf("failed to create dm-verity temp file: %w", err)
	}
	hashPath = hashTmp.Name()
	hashTmp.Close()

	params := verity.DefaultParams()
	params.HashName = dmVerityHashName
	params.DataBlockSize = blockSize
	params.HashBlockSize = blockSize
	params.HashType = 1
	params.DataBlocks = dataBlocks
	// HashAreaOffset places the hash tree after the 4KiB block containing the superblock.
	params.HashAreaOffset = uint64(blockSize)
	params.NoSuperblock = false
	uuidVal := uuid.New()
	copy(params.UUID[:], uuidVal[:])

	rootHash, err := verity.Create(&params, dataPath, hashPath)
	if err != nil {
		_ = os.Remove(hashPath)
		return "", "", fmt.Errorf("failed to create dm-verity hash tree: %w", err)
	}

	return hashPath, fmt.Sprintf("%s:%x", dmVerityHashName, rootHash), nil
}

// writeHashTreeAsSkippableFrame writes the hash tree file as a zstd skippable frame.
func writeHashTreeAsSkippableFrame(out *countingWriter, hashPath string) (frameOffset int64, bytesWritten int64, err error) {
	hashFile, err := os.Open(hashPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open dm-verity temp file: %w", err)
	}
	defer hashFile.Close()

	stHash, err := hashFile.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat dm-verity temp file: %w", err)
	}

	payloadSize := stHash.Size()
	if payloadSize > math.MaxUint32 {
		return 0, 0, fmt.Errorf("dm-verity payload size %d exceeds maximum skippable frame size: %w", payloadSize, errdefs.ErrOutOfRange)
	}

	frameOffset = out.Offset()
	if err := writeSkippableFrameHeader(out, zstdSkippableMagicMin, uint32(payloadSize)); err != nil {
		return 0, 0, err
	}

	copied, err := io.Copy(out, hashFile)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to write dm-verity payload: %w", err)
	}
	if copied != payloadSize {
		return 0, 0, fmt.Errorf("short copy while writing dm-verity payload: wrote %d, expected %d: %w", copied, payloadSize, errdefs.ErrFailedPrecondition)
	}

	return frameOffset, int64(zstdSkippableHeaderSizeBytes) + payloadSize, nil
}

func validateMaterializeDMVerityArgs(
	ctx context.Context,
	blob io.ReaderAt,
	blobSize int64,
	dmVerityFrameOffsetInBlob int64,
	layerPath string,
	rootDigest string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if blob == nil {
		return fmt.Errorf("dm-verity blob reader is nil: %w", errdefs.ErrInvalidArgument)
	}
	if blobSize <= 0 {
		return fmt.Errorf("invalid dm-verity blob size %d: %w", blobSize, errdefs.ErrInvalidArgument)
	}
	if dmVerityFrameOffsetInBlob < 0 {
		return fmt.Errorf("invalid dm-verity frame offset %d (must be >= 0): %w", dmVerityFrameOffsetInBlob, errdefs.ErrInvalidArgument)
	}
	if layerPath == "" {
		return fmt.Errorf("layer path is empty: %w", errdefs.ErrInvalidArgument)
	}
	if rootDigest == "" {
		return fmt.Errorf("dm-verity root digest is empty: %w", errdefs.ErrInvalidArgument)
	}
	if _, err := digest.Parse(rootDigest); err != nil {
		return fmt.Errorf("invalid dm-verity root digest %q: %w: %w", rootDigest, err, errdefs.ErrInvalidArgument)
	}

	// Validate that the skippable frame header fits within the blob.
	if dmVerityFrameOffsetInBlob+zstdSkippableHeaderSizeBytes > blobSize {
		return fmt.Errorf("dm-verity frame header out of bounds: offset=%d blobSize=%d: %w", dmVerityFrameOffsetInBlob, blobSize, errdefs.ErrOutOfRange)
	}

	return nil
}

// appendPayloadToLayerFile opens the layer file and appends the dm-verity payload.
func appendPayloadToLayerFile(ctx context.Context, layerPath string, payload io.Reader, payloadSize int64, blockSizeBytes int) (dmVerityOffsetInLayer int64, err error) {
	layerFile, err := os.OpenFile(layerPath, os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to open layer file: %w", err)
	}
	defer layerFile.Close()

	dmVerityOffsetInLayer, _, err = appendDMVerityPayloadToLayer(ctx, layerFile, payload, payloadSize, blockSizeBytes)
	if err != nil {
		return 0, fmt.Errorf("failed to append dm-verity payload to layer: %w", err)
	}
	return dmVerityOffsetInLayer, nil
}

// readDMVerityPayload returns a reader for exactly the dm-verity payload bytes within the blob.
// Caller (MaterializeDMVerity) is responsible for validating blob, blobSize, and offset via
// validateMaterializeDMVerityArgs before calling this function.
func readDMVerityPayload(blob io.ReaderAt, blobSize int64, dmVerityFrameOffsetInBlob int64) (payload *io.SectionReader, payloadSize int64, err error) {
	magic, n, err := readSkippableFrameHeader(blob, dmVerityFrameOffsetInBlob)
	if err != nil {
		return nil, 0, err
	}
	if !isZstdSkippableMagic(magic) {
		return nil, 0, fmt.Errorf("dm-verity header is not zstd skippable magic: 0x%08x: %w", magic, errdefs.ErrInvalidArgument)
	}

	payloadSize = int64(n)
	payloadOffset := dmVerityFrameOffsetInBlob + zstdSkippableHeaderSizeBytes
	if payloadOffset+payloadSize > blobSize {
		return nil, 0, fmt.Errorf("dm-verity payload out of bounds: [%d, %d) exceeds blobSize=%d: %w", payloadOffset, payloadOffset+payloadSize, blobSize, errdefs.ErrOutOfRange)
	}
	return io.NewSectionReader(blob, payloadOffset, payloadSize), payloadSize, nil
}

// appendDMVerityPayloadToLayer appends dm-verity payload bytes to the end of `layer.erofs`.
// The dm-verity data is appended to the same file as the EROFS image ("single device" mode),
// which allows dm-verity to verify the data using a single block device at mount time.
// Caller (MaterializeDMVerity) is responsible for validating arguments.
func appendDMVerityPayloadToLayer(ctx context.Context, layerFile *os.File, dmVerityPayload io.Reader, dmVerityPayloadSize int64, blockSizeBytes int) (dmVeritySuperblockOffsetInLayer int64, bytesWritten int64, err error) {
	// Seek to end of layer file
	currentLayerEndOffset, err := layerFile.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to seek layer to end: %w", err)
	}

	// Pad to block boundary
	// Ensure the dm-verity superblock offset is block-aligned for mount-time consumers.
	// This may pad the EROFS file with trailing zeros (which EROFS should ignore).
	padded, err := writeBlockPadding(layerFile, currentLayerEndOffset, blockSizeBytes)
	if err != nil {
		return 0, padded, fmt.Errorf("failed to write block padding for dm-verity: %w", err)
	}
	bytesWritten = padded
	dmVeritySuperblockOffset := currentLayerEndOffset + padded

	// Append dm-verity payload bytes
	// Copy the dm-verity superblock and hash tree from the source reader to the layer file.
	n, err := io.CopyN(layerFile, dmVerityPayload, dmVerityPayloadSize)
	if err != nil {
		return 0, bytesWritten + n, fmt.Errorf("failed to append dm-verity payload: %w", err)
	}
	bytesWritten += n
	return dmVeritySuperblockOffset, bytesWritten, nil
}

func dmVerityMetadataPath(layerPath string) string {
	// as per PR #12502, this is creating the path at which to
	// write a small JSON metadata file next to layer.erofs.
	return layerPath + ".dmverity"
}

// writeDMVerityMetadata writes a JSON file containing the DMVerityMetadata alongside `layer.erofs`.
// This matches the sidecar format from upstream PR #12502.
func writeDMVerityMetadata(ctx context.Context, layerPath string, meta DMVerityMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if layerPath == "" {
		return fmt.Errorf("layer path is empty: %w", errdefs.ErrInvalidArgument)
	}

	metaPath := dmVerityMetadataPath(layerPath)

	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dm-verity metadata: %w", err)
	}
	metaJSON = append(metaJSON, '\n')

	metaFile, err := atomicfile.New(metaPath, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create dm-verity metadata file: %w", err)
	}
	defer metaFile.Cancel()

	if _, err := metaFile.Write(metaJSON); err != nil {
		return fmt.Errorf("failed to write dm-verity metadata file: %w", err)
	}

	if err := metaFile.Close(); err != nil {
		return fmt.Errorf("failed to commit dm-verity metadata file: %w", err)
	}
	return nil
}

// writeBlockPadding writes zero padding to align to the given block size.
// It returns the number of bytes written (the padding amount).
// Since padding is always less than blockSize (which is a small power-of-two,
// e.g. 4096), a single write suffices.
func writeBlockPadding(w io.Writer, currentOffset int64, blockSize int) (int64, error) {
	if blockSize <= 0 {
		return 0, nil
	}

	remainder := currentOffset % int64(blockSize)
	if remainder == 0 {
		return 0, nil
	}
	pad := int64(blockSize) - remainder

	n, err := w.Write(make([]byte, pad))
	return int64(n), err
}

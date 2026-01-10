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
	"os"
)

/*
DM-Verity Payload (inside skippable frame payload)
┌──────────────────────────────────────────────────────────────────────────────┐
│ Superblock (512 bytes)                                                       │
│  - Contains ASCII "verity" signature somewhere near the start                │
├──────────────────────────────────────────────────────────────────────────────┤
│ Padding (3584 bytes)                                                         │
│  - Zero-filled padding so superblock region occupies 4096 bytes total        │
├──────────────────────────────────────────────────────────────────────────────┤
│ Merkle Tree (variable length)                                                │
│  - Hash tree computed over (possibly padded) uncompressed EROFS bytes        │
└──────────────────────────────────────────────────────────────────────────────┘
*/


type DMVerityMaterializeOptions struct {
	SuperblockPresent bool

	// BlockSizeBytes is the dm-verity data block size (typically 4096).
	// If zero, DefaultDMVerityBlockSize is assumed.
	BlockSizeBytes int

	// TODO(ac): other parameters if the superblock is not present?
	// or should we require superblock?
}

// WIP: assuming that we want to write out metadata for mount-time setup, to support
// useage as per: https://github.com/containerd/containerd/pull/12502/files
// 
// DMVerityMetadata is written alongside `layer.erofs` and consumed by mount-time setup.
type DMVerityMetadata struct {
	RootHashHex string `json:"roothash"`

	// DMVeritySuperblockOffsetInLayer is the absolute byte offset of the dm-verity superblock
	// (superblock-present mode) OR the hash-tree start (no-superblock mode) within `layer.erofs`.
	DMVeritySuperblockOffsetInLayer int64 `json:"hashoffset"`
	// NoSuperblock indicates no-superblock mode (payload begins with hash tree bytes).
	NoSuperblock bool `json:"nosuperblock,omitempty"`

	// BlockSizeBytes is the dm-verity block size in bytes (typically 4096).
	BlockSizeBytes int `json:"blocksize,omitempty"`
	HashName string `json:"hash,omitempty"`
	SaltHex string `json:"salt,omitempty"`
}

// MaterializeDMVerity is the entry point that the differ should call.
// 
// It extracts the dm-verity payload from the blob, appends it to `layer.erofs`,
// and writes a DMVerityMetadata file alongside `layer.erofs`.
//
// dmVerityPayloadOffsetInBlob corresponds to the OCI annotation `dmverity.offset` and points to the
// start of the dm-verity payload (i.e., skippable frame header start + 8).
func MaterializeDMVerity(
	ctx context.Context,
	blob io.ReaderAt,
	blobSize int64,
	dmVerityPayloadOffsetInBlob int64,
	layerPath string,
	rootHashHex string,
	opts DMVerityMaterializeOptions,
) (DMVerityMetadata, error) {
	_ = ctx
	_ = blob
	_ = blobSize
	_ = dmVerityPayloadOffsetInBlob
	_ = layerPath
	_ = rootHashHex
	_ = opts
	return DMVerityMetadata{}, ErrNotImplemented
}

// WriteDMVerityMetadata writes a JSON file containing the DMVerityMetadata alongside `layer.erofs`.
func WriteDMVerityMetadata(ctx context.Context, layerPath string, m DMVerityMetadata) error {
	_ = ctx
	_ = layerPath
	_ = m
	return ErrNotImplemented
}

// ReadDMVerityMetadata reads DMVerityMetadata written alongside `layer.erofs`.
func ReadDMVerityMetadata(ctx context.Context, layerPath string) (DMVerityMetadata, error) {
	_ = ctx
	_ = layerPath
	return DMVerityMetadata{}, ErrNotImplemented
}

// readDMVerityPayload returns a reader for exactly the dm-verity payload bytes within the blob.
func readDMVerityPayload(ctx context.Context, blob io.ReaderAt, blobSize int64, dmVerityPayloadOffsetInBlob int64) (payload *io.SectionReader, payloadSize int64, err error) {
	_ = ctx
	_ = blob
	_ = blobSize
	_ = dmVerityPayloadOffsetInBlob
	// payload = blob[dmVerityPayloadOffsetInBlob : payloadEnd]
	return nil, 0, ErrNotImplemented
}

// appendDMVerityPayloadToLayer appends dm-verity payload bytes to the end of `layer.erofs`.
// TODO(ac): I assume we want to run dm-verity in "single device" mode, meaning that we want
// to append the dm-verity payload to the same device as the EROFS image.
// We could also write the dm-verity payload to a separate file/device and record that in the metadata.
func appendDMVerityPayloadToLayer(ctx context.Context, layerFile *os.File, payload io.Reader, payloadSize int64) (dmVeritySuperblockOffsetInLayer int64, bytesWritten int64, err error) {
	_ = ctx
	_ = layerFile
	_ = payload
	_ = payloadSize
	return 0, 0, ErrNotImplemented
}

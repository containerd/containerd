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

// Defaults for `ctr images convert --to-seekable-erofs`
const (
	// DefaultChunkSize is the default uncompressed bytes per zstd frame 
	// This is the random access granularity
	DefaultChunkSize = 4 * 1024 * 1024 // 4 MiB

	// DefaultDMVerityBlockSize is the default data block size in bytes used by dm-verity.
	DefaultDMVerityBlockSize = 4096 // 4 KiB
)

type EncodeOptions struct {
	ChunkSizeBytes     int
	IncludeDMVerity    bool
	DMVeritySuperblock bool
};

type EncodeResult struct {
	TotalImageSizeBytes int64 
	UncompressedErofsSizeBytes int64 
	ZstdChunkSizeBytes int
	NumErofsFrames int 
	ChunkTableOffset int64
	DMVerityOffset int64
	DMVerityRootHashHex string
}

// EncodeErofsImageTo encodes a raw EROFS image into the seekable EROFS blob format.
//
// On-disk layout:
//   - normal zstd frames for EROFS bytes (one frame per uncompressed chunk)
//   - chunk table as a zstd skippable frame
//   - optional dm-verity payload as a zstd skippable frame at EOF
//
// [normal zstd frames: chunked EROFS image] [skippable: chunk table] [skippable: dm-verity data (optional)]
//
func EncodeErofsImageTo(ctx context.Context, erofsImage io.Reader, out io.Writer, opts EncodeOptions) (EncodeResult, error) {
	_ = ctx
	_ = erofsImage
	_ = out
	_ = opts
	return EncodeResult{}, ErrNotImplemented
}

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

// Package seekable implements encoding and decoding of seekable EROFS blobs.
//
// The seekable EROFS format wraps a raw EROFS filesystem image in a structure
// that supports random access without full decompression. The on-disk layout is:
//
//   - Normal zstd frames containing chunked EROFS image data
//   - A zstd skippable frame containing the chunk table (maps chunks to offsets)
//   - An optional zstd skippable frame containing dm-verity data
//
// The chunk table enables random access by recording the compressed offset and
// checksum of each zstd frame. Consumers can seek to any chunk, verify its
// integrity, and decompress only the needed data.
//
// Encoding takes a raw EROFS image and produces the seekable blob format.
// Decoding can be done either as a full stream, or per-chunk with chunk table
// lookups.
//
// The dm-verity support enables integrity verification of the uncompressed
// EROFS image at mount time using Linux dm-verity.
package seekable

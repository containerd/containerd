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

const (
	// AnnotationChunkTableOffset is the byte offset where the chunk table
	// skippable frame begins in the blob.
	AnnotationChunkTableOffset = "dev.containerd.erofs.zstd.chunk_table_offset"

	// AnnotationChunkDigest is the digest of the chunk table (header+entries).
	// Expected format is: `sha512:<hex>`.
	// Used to verify the integrity of the chunk table before parsing, as zstd
	// skippable frames do not have built-in checksums.
	AnnotationChunkDigest = "dev.containerd.erofs.zstd.chunk_digest"

	// AnnotationDMVerityOffset is the byte offset of the dm-verity frame in the blob.
	AnnotationDMVerityOffset = "dev.containerd.erofs.dmverity.offset"

	// AnnotationDMVerityRootDigest is the dm-verity root digest `<algo>:<hex>`.
	AnnotationDMVerityRootDigest = "dev.containerd.erofs.dmverity.root_digest"
)

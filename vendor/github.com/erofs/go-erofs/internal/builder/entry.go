// Package builder provides shared types for the mkfs sub-packages.
package builder

import "io"

// Entry carries extended metadata for a filesystem entry.
// Mode and Size come from fs.FileInfo; everything else lives here.
type Entry struct {
	UID, GID     uint32
	Mtime        uint64
	MtimeNs      uint32
	Nlink        uint32
	Rdev         uint32
	Xattrs       map[string]string
	LinkTarget   string
	Data         io.Reader // file content (full-image mode)
	Chunks       []Chunk   // physical block refs (metadata-only mode)
	Contiguous   bool      // data blocks are contiguous; flat-plain is sufficient
	MetadataOnly bool      // chunk-based layout even without chunks
}

// NullPhysicalBlock is the sentinel value for Chunk.PhysicalBlock that marks
// a hole (a sparse region of zero bytes). It corresponds to the on-disk
// EROFS null chunk encoding (StartBlkHi=0xFFFF, StartBlkLo=0xFFFFFFFF).
const NullPhysicalBlock uint64 = ^uint64(0)

// Chunk maps a range of logical blocks to physical blocks on a device.
// If PhysicalBlock == NullPhysicalBlock the chunk is a hole: Count logical
// blocks of zeros with no physical backing. DeviceID is ignored for holes.
type Chunk struct {
	PhysicalBlock uint64 // physical block address, or NullPhysicalBlock for a hole
	Count         uint16 // number of contiguous blocks
	DeviceID      uint16 // 0 = primary, 1+ = extra device; ignored for holes
}

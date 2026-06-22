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

// Chunk maps a range of logical blocks to physical blocks on a device.
type Chunk struct {
	PhysicalBlock uint64 // physical block address
	Count         uint16 // number of contiguous blocks
	DeviceID      uint16 // 0 = primary, 1+ = extra device
}

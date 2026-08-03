package erofs

import (
	"sort"

	"github.com/erofs/go-erofs/internal/disk"
)

// planLayout assigns NIDs and determines trailing data sizes for all entries.
func (w *erofsWriter) planLayout(root *erofsEntry) {
	// Collect all entries in a deterministic order (DFS, pre-order).
	// DFS keeps directory contents close to their parent inode,
	// improving locality for operations like find and ls -lR.
	w.entries = nil
	var walk func(e *erofsEntry)
	walk = func(e *erofsEntry) {
		w.entries = append(w.entries, e)
		if e.mode&disk.StatTypeMask == disk.StatTypeDir {
			sort.Slice(e.children, func(i, j int) bool {
				return e.children[i].name < e.children[j].name
			})
			for _, c := range e.children {
				walk(c)
			}
		}
	}
	walk(root)

	w.totalInodes = uint64(len(w.entries))

	// Block 0 holds: 1024-byte pad + 128-byte superblock + device slot(s) + padding
	// MetaBlkAddr is set later by write() depending on the on-disk layout.

	// Assign NIDs sequentially.
	// NID = byte offset from metaStartPos / 32.
	// Each extended inode is 64 bytes = 2 NID slots.
	// Trailing data follows and is padded to 32-byte boundary.
	currentOff := 0 // byte offset from metaStartPos
	for _, e := range w.entries {
		e.nid = uint64(currentOff / 32)
		e.xattrSize = calcXattrSize(e)

		// Decide compact (32B) vs extended (64B) inode.
		e.compact = e.uid <= 0xFFFF && e.gid <= 0xFFFF &&
			e.nlink <= 0xFFFF && e.size <= 0xFFFFFFFF &&
			e.mtime == w.buildTime && e.mtimeNs == 0

		inodeSize := disk.SizeInodeExtended
		if e.compact {
			inodeSize = disk.SizeInodeCompact
		}

		// The inode header region is inode core + xattr area.
		// Trailing data (dirents, chunk indexes, inline data) follows.
		headerSize := inodeSize + e.xattrSize

		// Determine layout
		switch e.mode & disk.StatTypeMask {
		case disk.StatTypeReg:
			switch {
			case e.size == 0 && len(e.chunks) == 0 && e.data == nil && !e.metadataOnly:
				e.layout = disk.LayoutFlatPlain
			case len(e.chunks) > 0 || e.metadataOnly:
				e.layout = disk.LayoutChunkBased
				if e.contiguous {
					e.chunkBits = w.minChunkBits(e.size)
				}
			default:
				// Full-image mode: decide inline vs plain
				if int(e.size) <= w.blockSize-headerSize {
					inBlockOff := (currentOff + headerSize) % w.blockSize
					if inBlockOff+int(e.size) <= w.blockSize {
						e.layout = disk.LayoutFlatInline
					} else {
						e.layout = disk.LayoutFlatPlain
					}
				} else {
					e.layout = disk.LayoutFlatPlain
				}
			}
		case disk.StatTypeDir:
			direntDataSize := w.direntDataSize(e)
			inBlockOff := (currentOff + headerSize) % w.blockSize
			if direntDataSize > 0 && inBlockOff+direntDataSize <= w.blockSize {
				e.layout = disk.LayoutFlatInline
			} else {
				e.layout = disk.LayoutFlatPlain
			}
		case disk.StatTypeSymlink:
			inBlockOff := (currentOff + headerSize) % w.blockSize
			if len(e.symTarget) > 0 && inBlockOff+len(e.symTarget) <= w.blockSize {
				e.layout = disk.LayoutFlatInline
			} else {
				e.layout = disk.LayoutFlatPlain
			}
		default:
			// Device files, fifos, sockets
			e.layout = disk.LayoutFlatPlain
		}

		// Recalculate trailing size now that layout is decided
		e.trailingSize = w.calcTrailingSize(e)

		totalInodeSize := headerSize + e.trailingSize
		// Pad to 32-byte boundary
		if totalInodeSize%32 != 0 {
			totalInodeSize = (totalInodeSize + 31) & ^31
		}

		// Check block boundary: inode core must not cross a block boundary
		blockOff := currentOff % w.blockSize
		if blockOff+inodeSize > w.blockSize {
			// Align to next block
			currentOff = (currentOff + w.blockSize - 1) & ^(w.blockSize - 1)
			e.nid = uint64(currentOff / 32)
		}

		// Also check that trailing data doesn't cross block boundary for inline layouts
		if e.layout == disk.LayoutFlatInline {
			blockOff = currentOff % w.blockSize
			if blockOff+headerSize+e.trailingSize > w.blockSize {
				// Fall back to flat-plain (data would cross block boundary)
				e.layout = disk.LayoutFlatPlain
				e.trailingSize = w.calcTrailingSize(e)
				totalInodeSize = headerSize + e.trailingSize
				if totalInodeSize%32 != 0 {
					totalInodeSize = (totalInodeSize + 31) & ^31
				}
			}
		}

		currentOff += totalInodeSize
	}

	w.rootNid = root.nid
}

// calcTrailingSize returns the number of bytes following the 64-byte inode.
func (w *erofsWriter) calcTrailingSize(e *erofsEntry) int {
	switch e.mode & disk.StatTypeMask {
	case disk.StatTypeReg:
		if e.layout == disk.LayoutChunkBased {
			if e.size == 0 && len(e.chunks) == 0 {
				return 0
			}
			cs := w.entryChunkSize(e)
			nchunks := (int(e.size) + cs - 1) / cs
			return nchunks * disk.SizeChunkIndex
		}
		if e.layout == disk.LayoutFlatInline {
			return int(e.size)
		}
		return 0
	case disk.StatTypeDir:
		if e.layout == disk.LayoutFlatInline {
			return w.direntDataSize(e)
		}
		return 0
	case disk.StatTypeSymlink:
		if e.layout == disk.LayoutFlatInline {
			return len(e.symTarget)
		}
		return 0
	default:
		return 0
	}
}

// direntNames returns the sorted list of dirent names for a directory,
// including "." and "..". EROFS requires dirents within each block to
// be sorted alphabetically.
func direntNames(e *erofsEntry) []string {
	names := make([]string, 0, len(e.children)+2)
	names = append(names, ".", "..")
	for _, c := range e.children {
		names = append(names, c.name)
	}
	sort.Strings(names)
	return names
}

// direntDataSize calculates the serialized EROFS dirent data size for a directory.
// For multi-block directories, this includes inter-block padding.
func (w *erofsWriter) direntDataSize(e *erofsEntry) int {
	names := direntNames(e)
	nEntries := len(names)
	if len(e.children) == 0 {
		// Empty dir still needs "." and ".." entries
		return 2*disk.SizeDirent + 1 + 2
	}

	totalSize := 0
	i := 0
	for i < nEntries {
		blockUsed := 0
		start := i
		nameSize := 0
		for j := i; j < nEntries; j++ {
			headerSize := (j - start + 1) * disk.SizeDirent
			nameSize += len(names[j])
			needed := headerSize + nameSize
			if needed > w.blockSize {
				break
			}
			blockUsed = needed
			i = j + 1
		}
		if i == start {
			blockUsed = disk.SizeDirent + len(names[i])
			i++
		}
		// Pad non-final blocks to block boundary
		if i < nEntries && blockUsed%w.blockSize != 0 {
			blockUsed = (blockUsed + w.blockSize - 1) & ^(w.blockSize - 1)
		}
		totalSize += blockUsed
	}

	return totalSize
}

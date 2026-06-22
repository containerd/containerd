package erofs

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"

	"github.com/erofs/go-erofs/internal/builder"
	"github.com/erofs/go-erofs/internal/disk"
)

// newMetaReader returns an at() function backed by an eagerly-read
// metadata buffer plus an on-demand block cache for data blocks
// outside the metadata region.
func newMetaReader(ra io.ReaderAt, metaStart, totalBytes int64, blockSize int) func(int64) []byte {
	metaSize := totalBytes - metaStart
	if metaSize <= 0 {
		return func(int64) []byte { return nil }
	}
	metaBuf := make([]byte, metaSize)
	if n, err := ra.ReadAt(metaBuf, metaStart); err != nil || int64(n) != metaSize {
		return func(int64) []byte { return nil }
	}

	cache := make(map[int64][]byte)

	return func(off int64) []byte {
		// Fast path: offset in metadata region.
		if off >= metaStart {
			o := off - metaStart
			if o >= int64(len(metaBuf)) {
				return nil
			}
			return metaBuf[o:]
		}
		// Outside metadata — flat-plain data block. Load on demand.
		if off < 0 || off >= totalBytes {
			return nil
		}
		blkAddr := off - off%int64(blockSize)
		if cached, ok := cache[blkAddr]; ok {
			return cached[off-blkAddr:]
		}
		sz := int64(blockSize)
		if blkAddr+sz > totalBytes {
			sz = totalBytes - blkAddr
		}
		buf := make([]byte, sz)
		if n, err := ra.ReadAt(buf, blkAddr); err != nil || int64(n) != sz {
			return nil
		}
		cache[blkAddr] = buf
		return buf[off-blkAddr:]
	}
}

// imgQEntry is a BFS queue entry for the image metadata walk.
type imgQEntry struct {
	nid  uint64
	path string
}

// copyFromImage is a fast path for CopyFrom when the source is an *image.
// Instead of walking via the fs.FS interface (which does per-inode ReadAt
// syscalls), it reads the entire metadata area into memory and parses
// inodes, directory entries, xattrs, and chunk indexes directly from the
// buffer. This reduces thousands of syscalls to a single ReadAt.
func (fsys *Writer) copyFromImage(img *image) error {
	metaStart := img.metaStartPos()
	totalBytes := int64(img.sb.Blocks) << img.sb.BlkSizeBits
	if totalBytes <= 0 {
		return nil
	}

	blkBits := img.sb.BlkSizeBits
	buildTime := img.sb.BuildTime
	buildTimeNs := img.sb.BuildTimeNs

	blockSize := int(1 << blkBits)

	// Get an accessor for image data. Reads the metadata region eagerly
	// and loads flat-plain data blocks on demand.
	at := newMetaReader(img.meta, metaStart, totalBytes, blockSize)

	// Shared xattr block address (if present). The at() function
	// will load the block on demand when xattrs are parsed.
	var sharedXattrOff int64
	if img.sb.XattrBlkAddr > 0 {
		sharedXattrOff = int64(img.sb.XattrBlkAddr) << blkBits
	}

	// Pre-allocate based on inode count from superblock.
	inodeCount := int(img.sb.Inos)
	if inodeCount == 0 {
		inodeCount = 64
	}
	queue := make([]imgQEntry, 0, inodeCount)
	queue = append(queue, imgQEntry{nid: uint64(img.sb.RootNid), path: "/"})

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Merge mode: process whiteout markers.
		if fsys.copyMerge && cur.path != "/" {
			base := path.Base(cur.path)
			if len(base) > len(whiteoutPrefix) && base[:len(whiteoutPrefix)] == whiteoutPrefix {
				if base == opaqueWhiteout {
					fsys.removeChildren(path.Dir(cur.path))
				} else {
					target := path.Dir(cur.path) + "/" + base[len(whiteoutPrefix):]
					if path.Dir(cur.path) == "/" {
						target = "/" + base[len(whiteoutPrefix):]
					}
					fsys.remove(target)
				}
				continue
			}
		}

		inodeAddr := metaStart + int64(cur.nid*disk.SizeInodeCompact)
		buf := at(inodeAddr)
		if len(buf) < disk.SizeInodeCompact {
			return fmt.Errorf("inode %d out of range", cur.nid)
		}

		format := binary.LittleEndian.Uint16(buf[:2])
		layout := uint8((format & 0x0E) >> 1)
		compact := format&0x01 == 0

		if compact && len(buf) < disk.SizeInodeCompact {
			return fmt.Errorf("compact inode %d out of range", cur.nid)
		}
		if !compact && len(buf) < disk.SizeInodeExtended {
			return fmt.Errorf("extended inode %d out of range", cur.nid)
		}

		var (
			mode    uint16
			uid     uint32
			gid     uint32
			nlink   uint32
			size    uint64
			idata   uint32
			mtime   uint64
			mtimeNs uint32
			xcnt    uint16
			icSize  int
		)

		if compact {
			var ino disk.InodeCompact
			if _, err := binary.Decode(buf[:disk.SizeInodeCompact], binary.LittleEndian, &ino); err != nil {
				return fmt.Errorf("decode compact inode %d: %w", cur.nid, err)
			}
			mode = ino.Mode
			uid = uint32(ino.UID)
			gid = uint32(ino.GID)
			nlink = uint32(ino.Nlink)
			size = uint64(ino.Size)
			idata = ino.InodeData
			mtime = buildTime
			mtimeNs = buildTimeNs
			xcnt = ino.XattrCount
			icSize = disk.SizeInodeCompact
		} else {
			var ino disk.InodeExtended
			if _, err := binary.Decode(buf[:disk.SizeInodeExtended], binary.LittleEndian, &ino); err != nil {
				return fmt.Errorf("decode extended inode %d: %w", cur.nid, err)
			}
			mode = ino.Mode
			uid = ino.UID
			gid = ino.GID
			nlink = ino.Nlink
			size = ino.Size
			idata = ino.InodeData
			mtime = ino.Mtime
			mtimeNs = ino.MtimeNs
			xcnt = ino.XattrCount
			icSize = disk.SizeInodeExtended
		}

		// Parse xattr area.
		xattrSize := 0
		if xcnt > 0 {
			xattrSize = int(xcnt-1)*disk.SizeXattrEntry + disk.SizeXattrBodyHeader
		}
		var xattrs map[string]string
		if xattrSize > 0 {
			xattrAddr := inodeAddr + int64(icSize)
			xb := at(xattrAddr)
			if len(xb) >= xattrSize {
				xattrs = parseXattrsFromBuf(xb[:xattrSize], at, sharedXattrOff, img.getLongPrefix)
			}
		}

		trailingAddr := inodeAddr + int64(icSize) + int64(xattrSize)
		typ := mode & disk.StatTypeMask

		// Build fsEntry directly, bypassing builder.Entry + add() overhead.
		fe := &fsEntry{
			path:    cur.path,
			mode:    mode,
			uid:     uid,
			gid:     gid,
			mtime:   mtime,
			mtimeNs: mtimeNs,
			size:    size,
			xattrs:  xattrs,
		}
		if nlink > 0 {
			fe.nlink = nlink
			fe.nlinkSet = true
		}
		fe.fileClosed = true
		if fsys.copyMetadataOnly {
			fe.metadataOnly = true
		}

		switch typ {
		case disk.StatTypeDir:
			dirSize := int(size)
			if dirSize > 0 {
				var dirData []byte
				switch layout {
				case disk.LayoutFlatPlain:
					dataAddr := int64(idata) << blkBits
					d := at(dataAddr)
					if d != nil && len(d) >= dirSize {
						dirData = d[:dirSize]
					} else {
						dirData = make([]byte, dirSize)
						if _, err := img.meta.ReadAt(dirData, dataAddr); err != nil {
							return fmt.Errorf("read dir data for nid %d: %w", cur.nid, err)
						}
					}
				case disk.LayoutFlatInline:
					d := at(trailingAddr)
					if d != nil && len(d) >= dirSize {
						dirData = d[:dirSize]
					}
				}
				if dirData != nil {
					fsys.parseDirBlock(dirData, dirSize, blockSize, cur.path, &queue)
				}
			}

		case disk.StatTypeSymlink:
			if size > 0 {
				var linkData []byte
				if layout == disk.LayoutFlatPlain {
					linkData = make([]byte, size)
					if _, err := img.meta.ReadAt(linkData, int64(idata)<<blkBits); err != nil {
						return fmt.Errorf("read symlink data for nid %d: %w", cur.nid, err)
					}
				} else {
					linkData = at(trailingAddr)
				}
				if linkData != nil && int(size) <= len(linkData) {
					fe.linkTarget = string(linkData[:size])
				}
			}

		case disk.StatTypeReg:
			if layout == disk.LayoutChunkBased && size > 0 {
				chunkFmt := uint16(idata)
				if chunkFmt&disk.LayoutChunkFormatIndexes != 0 {
					chunkAddr := trailingAddr
					if chunkAddr%8 != 0 {
						chunkAddr = (chunkAddr + 7) & ^int64(7)
					}
					fe.chunks = fsys.parseChunks(at(chunkAddr), chunkFmt, size, blkBits, img.deviceIDMask)
					fe.contiguous = true
				}
			}

		case disk.StatTypeChrdev, disk.StatTypeBlkdev:
			fe.rdev = disk.RdevFromMode(mode, idata)
		}

		// Remap chunk DeviceIDs for metadata-only sources.
		if fsys.copyMetadataOnly && fsys.copyDeviceID > 0 {
			offset := fsys.copyDeviceID - 1
			for i := range fe.chunks {
				fe.chunks[i].DeviceID += offset
			}
		}

		// Register in the tree.
		if cur.path == "/" {
			// Update root metadata.
			fsys.root.mode = fe.mode
			fsys.root.uid = fe.uid
			fsys.root.gid = fe.gid
			fsys.root.mtime = fe.mtime
			fsys.root.mtimeNs = fe.mtimeNs
			fsys.root.nlink = fe.nlink
			fsys.root.nlinkSet = fe.nlinkSet
			fsys.root.xattrs = fe.xattrs
		} else if existing, ok := fsys.byPath[cur.path]; ok {
			// Merge overwrites: preserve tree linkage.
			savedParent := existing.parent
			savedChildren := existing.children
			*existing = *fe
			existing.parent = savedParent
			existing.children = savedChildren
		} else {
			fsys.addChild(fe)
		}
	}
	return nil
}

// parseDirBlock extracts directory entries from dirent data and enqueues
// child inodes for BFS traversal.
func (fsys *Writer) parseDirBlock(data []byte, dirSize, blockSize int, parentPath string, queue *[]imgQEntry) {
	pos := 0
	for pos < dirSize {
		blockEnd := pos + blockSize
		if blockEnd > dirSize {
			blockEnd = dirSize
		}
		blk := data[pos:blockEnd]
		if len(blk) < disk.SizeDirent {
			break
		}

		firstNameOff := binary.LittleEndian.Uint16(blk[8:10])
		nEntries := int(firstNameOff / disk.SizeDirent)
		if nEntries == 0 || nEntries*disk.SizeDirent > len(blk) {
			break
		}

		for i := 0; i < nEntries; i++ {
			off := i * disk.SizeDirent
			nid := binary.LittleEndian.Uint64(blk[off : off+8])
			nameOff := int(binary.LittleEndian.Uint16(blk[off+8 : off+10]))

			var nameEnd int
			if i < nEntries-1 {
				nameEnd = int(binary.LittleEndian.Uint16(blk[(i+1)*disk.SizeDirent+8 : (i+1)*disk.SizeDirent+10]))
			} else {
				nameEnd = len(blk)
			}
			if nameOff >= len(blk) || nameEnd > len(blk) || nameOff >= nameEnd {
				continue
			}

			// Extract name, trimming trailing NUL padding.
			nameBytes := blk[nameOff:nameEnd]
			for len(nameBytes) > 0 && nameBytes[len(nameBytes)-1] == 0 {
				nameBytes = nameBytes[:len(nameBytes)-1]
			}
			name := string(nameBytes)
			if name == "." || name == ".." || name == "" {
				continue
			}

			childPath := parentPath + "/" + name
			if parentPath == "/" {
				childPath = "/" + name
			}
			*queue = append(*queue, imgQEntry{nid: nid, path: childPath})
		}

		pos = blockEnd
	}
}

// parseChunks extracts chunk index entries from an in-memory buffer.
func (fsys *Writer) parseChunks(data []byte, chunkFmt uint16, fileSize uint64, blkBits uint8, deviceIDMask uint16) []builder.Chunk {
	chunkBits := blkBits + uint8(chunkFmt&disk.LayoutChunkFormatBits)
	nchunks := int((fileSize-1)>>chunkBits) + 1
	blocksPerChunk := 1 << (chunkBits - blkBits)

	// Align to 8 bytes for index entries.
	needed := nchunks * disk.SizeChunkIndex
	if len(data) < needed {
		return nil
	}

	chunks := make([]builder.Chunk, 0, nchunks)
	for i := range nchunks {
		off := i * disk.SizeChunkIndex
		startBlkLo := binary.LittleEndian.Uint32(data[off+4 : off+8])
		if ^startBlkLo == 0 {
			continue // null/hole
		}
		startBlkHi := binary.LittleEndian.Uint16(data[off : off+2])
		deviceID := binary.LittleEndian.Uint16(data[off+2:off+4]) & deviceIDMask
		physBlock := (uint64(startBlkHi) << 32) | uint64(startBlkLo)

		if len(chunks) > 0 {
			prev := &chunks[len(chunks)-1]
			if prev.DeviceID == deviceID &&
				prev.PhysicalBlock+uint64(prev.Count) == physBlock &&
				int(prev.Count)+blocksPerChunk <= 65535 {
				prev.Count += uint16(blocksPerChunk)
				continue
			}
		}
		chunks = append(chunks, builder.Chunk{
			PhysicalBlock: physBlock,
			Count:         uint16(blocksPerChunk),
			DeviceID:      deviceID,
		})
	}
	return chunks
}

// parseXattrsFromBuf parses xattr entries from an in-memory buffer.
// at provides on-demand access to the shared xattr block at sharedOff.
// longPrefix resolves long xattr prefix indexes (NameIndex with high bit set).
func parseXattrsFromBuf(buf []byte, at func(int64) []byte, sharedOff int64, longPrefix func(uint8) (string, error)) map[string]string {
	if len(buf) < disk.SizeXattrBodyHeader {
		return nil
	}

	var xh disk.XattrHeader
	if _, err := binary.Decode(buf[:disk.SizeXattrBodyHeader], binary.LittleEndian, &xh); err != nil {
		return nil
	}
	pos := disk.SizeXattrBodyHeader

	xattrs := make(map[string]string)

	// Resolve shared xattr references.
	for i := 0; i < int(xh.SharedCount) && pos+4 <= len(buf); i++ {
		idx := binary.LittleEndian.Uint32(buf[pos : pos+4])
		pos += 4

		if sharedOff == 0 {
			continue
		}
		sharedBlock := at(sharedOff + int64(idx)*4)
		if sharedBlock == nil || len(sharedBlock) < disk.SizeXattrEntry {
			continue
		}
		var xe disk.XattrEntry
		if _, err := binary.Decode(sharedBlock[:disk.SizeXattrEntry], binary.LittleEndian, &xe); err != nil {
			continue
		}
		entryLen := int(xe.NameLen) + int(xe.ValueLen)
		if disk.SizeXattrEntry+entryLen > len(sharedBlock) {
			continue
		}
		sb := sharedBlock[disk.SizeXattrEntry:]
		name := xattrName(xe, sb[:xe.NameLen], longPrefix)
		value := string(sb[xe.NameLen : int(xe.NameLen)+int(xe.ValueLen)])
		xattrs[name] = value
	}

	// Parse inline xattr entries.
	for pos+disk.SizeXattrEntry <= len(buf) {
		var xe disk.XattrEntry
		if _, err := binary.Decode(buf[pos:pos+disk.SizeXattrEntry], binary.LittleEndian, &xe); err != nil {
			break
		}
		pos += disk.SizeXattrEntry

		entryLen := int(xe.NameLen) + int(xe.ValueLen)
		if pos+entryLen > len(buf) {
			break
		}

		name := xattrName(xe, buf[pos:pos+int(xe.NameLen)], longPrefix)
		pos += int(xe.NameLen)
		value := string(buf[pos : pos+int(xe.ValueLen)])
		pos += int(xe.ValueLen)

		xattrs[name] = value

		// Round up to 4-byte boundary.
		if rem := pos % 4; rem != 0 {
			pos += 4 - rem
		}
	}
	if len(xattrs) == 0 {
		return nil
	}
	return xattrs
}

// xattrName builds the full xattr name from an entry and its raw name bytes.
// longPrefix resolves long prefix indexes when the high bit of NameIndex is set.
func xattrName(xe disk.XattrEntry, rawName []byte, longPrefix func(uint8) (string, error)) string {
	var prefix string
	if xe.NameIndex&0x80 != 0 {
		// Long prefix: high bit set, low 7 bits index the prefix table.
		if longPrefix != nil {
			if p, err := longPrefix(xe.NameIndex & 0x7F); err == nil {
				prefix = p
			}
		}
	} else if xe.NameIndex != 0 {
		prefix = xattrIndex(xe.NameIndex).String()
	}
	return prefix + string(rawName)
}

package erofs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sort"

	"github.com/erofs/go-erofs/internal/disk"
)

// maxBlockSize is the largest block size we support.
const maxBlockSize = 1 << 20

// onlyWriter wraps an io.Writer to hide io.ReaderFrom so that
// io.CopyBuffer uses the caller-provided buffer instead of
// the destination's ReadFrom (which allocates its own).
type onlyWriter struct{ io.Writer }

// erofsWriter serializes EROFS metadata to an io.Writer.
type erofsWriter struct {
	entries     []*erofsEntry // all entries in NID order
	rootNid     uint64
	metaBlkAddr uint32
	totalInodes uint64
	buildTime   uint64
	buildTimeNs uint32
	devices     []uint64 // per-device block counts (one slot per entry)
	blockSize   int
	chunkBits   uint8                        // log2(chunkSize / blockSize); chunkSize = blockSize << chunkBits
	copyBuf     []byte                       // reusable buffer for io.CopyBuffer
	zeroBuf     []byte                       // blockSize-length zero buffer for padding
	inodeBuf    [disk.SizeInodeExtended]byte // scratch buffer for writeInode
}

// inodeSize returns the on-disk inode header size for e.
func inodeCoreSize(e *erofsEntry) int {
	if e.compact {
		return disk.SizeInodeCompact
	}
	return disk.SizeInodeExtended
}

// entryChunkBits returns the chunk bits for a specific entry.
// Contiguous entries use a larger chunk size to minimize chunk indexes.
func (w *erofsWriter) entryChunkBits(e *erofsEntry) uint8 {
	if e.chunkBits > 0 {
		return e.chunkBits
	}
	return w.chunkBits
}

// entryChunkSize returns the chunk size in bytes for a specific entry.
func (w *erofsWriter) entryChunkSize(e *erofsEntry) int {
	return w.blockSize << w.entryChunkBits(e)
}

// minChunkBits returns the minimum chunkBits such that file size fits in
// one chunk (chunkSize >= size). Capped at 31 (LayoutChunkFormatBits max).
func (w *erofsWriter) minChunkBits(size uint64) uint8 {
	bits := w.chunkBits
	for uint64(w.blockSize)<<bits < size && bits < 31 {
		bits++
	}
	return bits
}

func (w *erofsWriter) write(out io.WriteSeeker) error {
	w.copyBuf = make([]byte, 256*1024) // shared io.CopyBuffer buffer
	return w.writeSeekable(out)
}

// writeSeekable uses a data-first on-disk layout: block0 (placeholder),
// data blocks, metadata. After everything is written, it seeks back to
// write the real superblock. This matches how mkfs.erofs lays out
// streaming sources — data is written as it arrives, metadata last.
func (w *erofsWriter) writeSeekable(out io.WriteSeeker) error {
	// Data-first layout: sbArea, data blocks, metadata.
	// Set metaBlkAddr to a sentinel so assignDataBlocks uses data-first.
	w.metaBlkAddr = 0xFFFFFFFF
	w.assignDataBlocks()

	// Write placeholder superblock area.
	if _, err := out.Write(make([]byte, w.sbAreaSize())); err != nil {
		return err
	}

	// Stream data blocks directly to output.
	if err := w.writeDataBlocks(out); err != nil {
		return err
	}

	// Buffer and write metadata.
	meta := w.newMetaBuffer()
	if err := w.writeMetadataInodes(meta); err != nil {
		return err
	}
	if _, err := meta.WriteTo(out); err != nil {
		return err
	}

	// Seek back and write the real block 0 (superblock).
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return w.writeBlock0(out)
}

// newMetaBuffer returns a pre-sized bytes.Buffer for metadata serialization.
func (w *erofsWriter) newMetaBuffer() *bytes.Buffer {
	totalMetaBytes := 0
	for _, e := range w.entries {
		isz := disk.SizeInodeExtended
		if e.compact {
			isz = disk.SizeInodeCompact
		}
		sz := isz + e.xattrSize + e.trailingSize
		if sz%32 != 0 {
			sz = (sz + 31) & ^31
		}
		totalMetaBytes += sz
	}
	// SB area + metadata padded to block boundary.
	capacity := w.blockSize + ((totalMetaBytes + w.blockSize - 1) & ^(w.blockSize - 1))
	buf := bytes.NewBuffer(make([]byte, 0, capacity))
	return buf
}

// assignDataBlocks assigns data block addresses to flat-plain entries.
// For metadata-first layout, data follows metadata.
// For data-first layout, data starts after the superblock area.
func (w *erofsWriter) assignDataBlocks() {
	sbBlks := w.sbAreaBlocks()
	if w.metaBlkAddr == uint32(sbBlks) {
		// Metadata-first: data blocks come after metadata.
		totalMetaBytes := 0
		for _, e := range w.entries {
			expectedOff := int(e.nid) * 32
			sz := inodeCoreSize(e) + e.xattrSize + e.trailingSize
			if sz%32 != 0 {
				sz = (sz + 31) & ^31
			}
			end := expectedOff + sz
			if end > totalMetaBytes {
				totalMetaBytes = end
			}
		}
		metaBlocks := (totalMetaBytes + w.blockSize - 1) / w.blockSize
		addr := uint32(w.sbAreaBlocks() + metaBlocks)
		for _, e := range w.entries {
			if ds := w.flatPlainDataSize(e); ds > 0 {
				e.dataBlkAddr = addr
				addr += uint32((ds + w.blockSize - 1) / w.blockSize)
			}
		}
	} else {
		// Data-first: data starts after superblock area.
		addr := uint32(w.sbAreaBlocks())
		for _, e := range w.entries {
			if ds := w.flatPlainDataSize(e); ds > 0 {
				e.dataBlkAddr = addr
				addr += uint32((ds + w.blockSize - 1) / w.blockSize)
			}
		}
		w.metaBlkAddr = addr // metadata follows data
	}
}

// sbAreaSize returns the number of bytes needed for the superblock area
// (blocks before metadata): 1024-byte pad + superblock + device slots,
// rounded up to block boundary.
func (w *erofsWriter) sbAreaSize() int {
	n := disk.SuperBlockOffset + disk.SizeSuperBlock
	if len(w.devices) > 0 {
		n += len(w.devices) * disk.SizeDeviceSlot
	}
	return ((n + w.blockSize - 1) / w.blockSize) * w.blockSize
}

// sbAreaBlocks returns the number of blocks occupied by the superblock area.
func (w *erofsWriter) sbAreaBlocks() int {
	return w.sbAreaSize() / w.blockSize
}

// metadataBytes computes the total size of the metadata area, including
// any zero-padding inserted to reach each inode's expected offset (NID * 32)
// and rounding each entry up to a 32-byte boundary.
func (w *erofsWriter) metadataBytes() int {
	curOff := 0
	for _, e := range w.entries {
		expectedOff := int(e.nid) * 32
		if curOff < expectedOff {
			curOff = expectedOff
		}
		sz := inodeCoreSize(e) + e.xattrSize + e.trailingSize
		if rem := sz % 32; rem != 0 {
			sz += 32 - rem
		}
		curOff += sz
	}
	return curOff
}

func (w *erofsWriter) writeBlock0(buf io.Writer) error {
	sbArea := make([]byte, w.sbAreaSize())

	totalMetaBytes := w.metadataBytes()
	metaBlocks := (totalMetaBytes + w.blockSize - 1) / w.blockSize

	// Count data blocks.
	dataBlocks := 0
	for _, e := range w.entries {
		if ds := w.flatPlainDataSize(e); ds > 0 {
			dataBlocks += (ds + w.blockSize - 1) / w.blockSize
		}
	}
	totalBlocks := w.sbAreaBlocks() + metaBlocks + dataBlocks

	var featureIncompat uint32
	var extraDevices uint16
	var devtSlotOff uint16

	if len(w.devices) > 0 {
		featureIncompat |= disk.FeatureIncompatDeviceTable
		extraDevices = uint16(len(w.devices))
		devtSlotOff = uint16(disk.SizeSuperBlock / 16)
	}
	for _, e := range w.entries {
		if len(e.chunks) > 0 {
			featureIncompat |= disk.FeatureIncompatChunkedFile
			break
		}
	}

	sb := disk.SuperBlock{
		MagicNumber:     disk.MagicNumber,
		BlkSizeBits:     blkBits(w.blockSize),
		RootNid:         uint16(w.rootNid),
		Inos:            w.totalInodes,
		BuildTime:       w.buildTime,
		BuildTimeNs:     w.buildTimeNs,
		Blocks:          uint32(totalBlocks),
		MetaBlkAddr:     w.metaBlkAddr,
		FeatureIncompat: featureIncompat,
		ExtraDevices:    extraDevices,
		DevtSlotOff:     devtSlotOff,
	}

	sbBuf := &bytes.Buffer{}
	if err := binary.Write(sbBuf, binary.LittleEndian, &sb); err != nil {
		return fmt.Errorf("write superblock: %w", err)
	}
	copy(sbArea[disk.SuperBlockOffset:], sbBuf.Bytes())

	// Write device slots right after superblock.
	for i, blocks := range w.devices {
		if blocks > math.MaxUint32 {
			return fmt.Errorf("device %d block count %d exceeds 32-bit limit", i+1, blocks)
		}
		devSlot := disk.DeviceSlot{
			Blocks: uint32(blocks),
		}
		devBuf := &bytes.Buffer{}
		if err := binary.Write(devBuf, binary.LittleEndian, &devSlot); err != nil {
			return fmt.Errorf("write device slot: %w", err)
		}
		off := disk.SuperBlockOffset + disk.SizeSuperBlock + i*disk.SizeDeviceSlot
		copy(sbArea[off:], devBuf.Bytes())
	}

	_, err := buf.Write(sbArea)
	return err
}

// writeMetadataInodes writes inode metadata. Data block addresses must
// already be assigned on each entry before calling this method.
func (w *erofsWriter) writeMetadataInodes(buf io.Writer) error {
	metaStart := 0
	for _, e := range w.entries {
		expectedOff := int(e.nid) * 32
		if expectedOff > metaStart {
			if _, err := buf.Write(w.zeroBuf[:expectedOff-metaStart]); err != nil {
				return err
			}
			metaStart = expectedOff
		}

		if err := w.writeInode(buf, e); err != nil {
			return fmt.Errorf("write inode for %s: %w", e.path, err)
		}
		if e.compact {
			metaStart += disk.SizeInodeCompact
		} else {
			metaStart += disk.SizeInodeExtended
		}

		// Write xattr area
		if e.xattrSize > 0 {
			if err := w.writeXattrs(buf, e); err != nil {
				return fmt.Errorf("write xattrs for %s: %w", e.path, err)
			}
			metaStart += e.xattrSize
		}

		// Write trailing data
		switch e.mode & disk.StatTypeMask {
		case disk.StatTypeReg:
			if e.layout == disk.LayoutChunkBased && (e.size > 0 || len(e.chunks) > 0) {
				if err := w.writeChunkIndexes(buf, e); err != nil {
					return fmt.Errorf("write chunks for %s: %w", e.path, err)
				}
				metaStart += e.trailingSize
			} else if e.layout == disk.LayoutFlatInline && e.size > 0 && e.data != nil {
				n, err := io.CopyBuffer(onlyWriter{buf}, io.LimitReader(e.data, int64(e.size)), w.copyBuf)
				if c, ok := e.data.(io.Closer); ok {
					_ = c.Close()
				}
				if err != nil {
					return fmt.Errorf("write inline data for %s: %w", e.path, err)
				}
				metaStart += int(n)
			}
		case disk.StatTypeDir:
			if e.layout == disk.LayoutFlatInline {
				n, err := w.writeDirents(buf, e)
				if err != nil {
					return fmt.Errorf("write dirents for %s: %w", e.path, err)
				}
				metaStart += n
			}
		case disk.StatTypeSymlink:
			if e.layout == disk.LayoutFlatInline {
				if _, err := io.WriteString(buf, e.symTarget); err != nil {
					return fmt.Errorf("write symlink for %s: %w", e.path, err)
				}
				metaStart += len(e.symTarget)
			}
		}

		// Pad to 32-byte boundary
		inodeSize := disk.SizeInodeExtended
		if e.compact {
			inodeSize = disk.SizeInodeCompact
		}
		totalWritten := inodeSize + e.xattrSize + e.trailingSize
		if totalWritten%32 != 0 {
			padSize := 32 - (totalWritten % 32)
			if _, err := buf.Write(w.zeroBuf[:padSize]); err != nil {
				return err
			}
			metaStart += padSize
		}
	}

	// Pad metadata to full block boundary
	if metaStart%w.blockSize != 0 {
		padSize := w.blockSize - (metaStart % w.blockSize)
		if _, err := buf.Write(w.zeroBuf[:padSize]); err != nil {
			return err
		}
	}

	return nil
}

func (w *erofsWriter) writeInode(buf io.Writer, e *erofsEntry) error {
	var inodeData uint32

	switch e.mode & disk.StatTypeMask {
	case disk.StatTypeReg:
		if e.layout == disk.LayoutChunkBased {
			inodeData = disk.LayoutChunkFormatIndexes | uint32(w.entryChunkBits(e))
		} else if e.layout == disk.LayoutFlatPlain && e.size > 0 {
			inodeData = e.dataBlkAddr
		}
	case disk.StatTypeDir, disk.StatTypeSymlink:
		if e.layout == disk.LayoutFlatPlain {
			inodeData = e.dataBlkAddr
		}
	case disk.StatTypeChrdev, disk.StatTypeBlkdev, disk.StatTypeFifo, disk.StatTypeSock:
		inodeData = e.rdev
	}

	fileSize := e.size
	switch e.mode & disk.StatTypeMask {
	case disk.StatTypeDir:
		fileSize = uint64(w.direntDataSize(e))
	case disk.StatTypeSymlink:
		fileSize = uint64(len(e.symTarget))
	}

	b := &w.inodeBuf
	clear(b[:])

	if e.compact {
		binary.LittleEndian.PutUint16(b[0:2], inodeFormat(e.layout, true))
		binary.LittleEndian.PutUint16(b[2:4], xattrCount(e.xattrSize))
		binary.LittleEndian.PutUint16(b[4:6], e.mode)
		binary.LittleEndian.PutUint16(b[6:8], uint16(e.nlink))
		binary.LittleEndian.PutUint32(b[8:12], uint32(fileSize))
		binary.LittleEndian.PutUint32(b[16:20], inodeData)
		binary.LittleEndian.PutUint16(b[24:26], uint16(e.uid))
		binary.LittleEndian.PutUint16(b[26:28], uint16(e.gid))
		_, err := buf.Write(b[:disk.SizeInodeCompact])
		return err
	}

	binary.LittleEndian.PutUint16(b[0:2], inodeFormat(e.layout, false))
	binary.LittleEndian.PutUint16(b[2:4], xattrCount(e.xattrSize))
	binary.LittleEndian.PutUint16(b[4:6], e.mode)
	binary.LittleEndian.PutUint64(b[8:16], fileSize)
	binary.LittleEndian.PutUint32(b[16:20], inodeData)
	binary.LittleEndian.PutUint32(b[24:28], e.uid)
	binary.LittleEndian.PutUint32(b[28:32], e.gid)
	binary.LittleEndian.PutUint64(b[32:40], e.mtime)
	binary.LittleEndian.PutUint32(b[40:44], e.mtimeNs)
	binary.LittleEndian.PutUint32(b[44:48], e.nlink)
	_, err := buf.Write(b[:disk.SizeInodeExtended])
	return err
}

func (w *erofsWriter) writeXattrs(buf io.Writer, e *erofsEntry) error {
	// XattrHeader: 4-byte name filter + 1-byte shared count + 7 reserved = 12 bytes
	var xhdr [12]byte
	binary.LittleEndian.PutUint32(xhdr[0:4], 0xFFFFFFFF) // name filter unused
	if _, err := buf.Write(xhdr[:]); err != nil {
		return err
	}

	for _, name := range sortedXattrKeys(e.xattrs) {
		value := e.xattrs[name]
		nameIndex, suffix := xattrSplit(name)

		var xent [disk.SizeXattrEntry]byte
		xent[0] = uint8(len(suffix))
		xent[1] = nameIndex
		binary.LittleEndian.PutUint16(xent[2:4], uint16(len(value)))
		if _, err := buf.Write(xent[:]); err != nil {
			return err
		}
		if _, err := io.WriteString(buf, suffix); err != nil {
			return err
		}
		if _, err := io.WriteString(buf, value); err != nil {
			return err
		}

		// Pad to 4-byte boundary
		entryLen := disk.SizeXattrEntry + len(suffix) + len(value)
		if entryLen%4 != 0 {
			if _, err := buf.Write(w.zeroBuf[:4-entryLen%4]); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeChunkIndexes writes chunk index entries for a regular file.
// Each index entry covers one logical chunk (chunkSize bytes).
func (w *erofsWriter) writeChunkIndexes(buf io.Writer, e *erofsEntry) error {
	cs := w.entryChunkSize(e)
	blocksPerChunk := cs / w.blockSize
	nchunks := (int(e.size) + cs - 1) / cs

	// Null chunk index (no mapping): StartBlkHi=0xFFFF, DeviceID=0, StartBlkLo=NullAddr.
	var nullIdx [disk.SizeChunkIndex]byte
	binary.LittleEndian.PutUint16(nullIdx[0:2], 0xFFFF)
	binary.LittleEndian.PutUint32(nullIdx[4:8], nullAddr)

	if len(e.chunks) > 0 {
		// Walk source chunks and emit one index per logical chunk.
		// Source chunks use block-granularity counts; we step by blocksPerChunk.
		var scratch [disk.SizeChunkIndex]byte
		ci := 0   // index into source chunks
		coff := 0 // block offset within current source chunk
		for n := 0; n < nchunks; n++ {
			if ci >= len(e.chunks) {
				if _, err := buf.Write(nullIdx[:]); err != nil {
					return err
				}
				continue
			}
			c := e.chunks[ci]
			phys := c.PhysicalBlock + uint64(coff)
			binary.LittleEndian.PutUint16(scratch[0:2], uint16(phys>>32))
			binary.LittleEndian.PutUint16(scratch[2:4], c.DeviceID)
			binary.LittleEndian.PutUint32(scratch[4:8], uint32(phys))
			if _, err := buf.Write(scratch[:]); err != nil {
				return err
			}
			coff += blocksPerChunk
			for ci < len(e.chunks) && coff >= int(e.chunks[ci].Count) {
				coff -= int(e.chunks[ci].Count)
				ci++
			}
		}
	} else {
		for n := 0; n < nchunks; n++ {
			if _, err := buf.Write(nullIdx[:]); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeDirents writes EROFS directory entries packed into block-sized chunks.
func (w *erofsWriter) writeDirents(buf io.Writer, e *erofsEntry) (int, error) {
	type direntInfo struct {
		name     string
		nid      uint64
		fileType uint8
	}

	// Build the full entry list including "." and ".." then sort
	// alphabetically. EROFS requires dirents to be sorted within
	// each block; "." and ".." are not guaranteed to be first.
	allEnts := make([]direntInfo, 0, len(e.children)+2)
	allEnts = append(allEnts, direntInfo{".", e.nid, disk.FileTypeDir})
	allEnts = append(allEnts, direntInfo{"..", e.parentNid, disk.FileTypeDir})
	for _, c := range e.children {
		allEnts = append(allEnts, direntInfo{
			name:     c.name,
			nid:      c.nid,
			fileType: c.erofsFileType,
		})
	}
	sort.Slice(allEnts, func(i, j int) bool {
		return allEnts[i].name < allEnts[j].name
	})

	totalWritten := 0
	i := 0
	for i < len(allEnts) {
		// Determine how many entries fit in this block
		start := i
		blockUsed := 0
		nameSize := 0
		for j := i; j < len(allEnts); j++ {
			headerSize := (j - start + 1) * disk.SizeDirent
			nameSize += len(allEnts[j].name)
			needed := headerSize + nameSize
			if needed > w.blockSize {
				break
			}
			blockUsed = needed
			i = j + 1
		}
		if i == start {
			// Single entry too large for a block (shouldn't happen)
			blockUsed = disk.SizeDirent + len(allEnts[i].name)
			i++
		}

		blockEnts := allEnts[start:i]
		blockHeaderSize := len(blockEnts) * disk.SizeDirent

		// Write dirent headers
		var scratch [disk.SizeDirent]byte
		nameOff := uint16(blockHeaderSize)
		for j, de := range blockEnts {
			if j > 0 {
				nameOff += uint16(len(blockEnts[j-1].name))
			}
			binary.LittleEndian.PutUint64(scratch[0:8], de.nid)
			binary.LittleEndian.PutUint16(scratch[8:10], nameOff)
			scratch[10] = de.fileType
			scratch[11] = 0
			if _, err := buf.Write(scratch[:]); err != nil {
				return totalWritten, err
			}
			totalWritten += disk.SizeDirent
		}

		// Write names
		for _, de := range blockEnts {
			n, err := io.WriteString(buf, de.name)
			if err != nil {
				return totalWritten, err
			}
			totalWritten += n
		}

		// Pad to block boundary if there are more entries
		if i < len(allEnts) && blockUsed%w.blockSize != 0 {
			padSize := w.blockSize - (blockUsed % w.blockSize)
			if _, err := buf.Write(w.zeroBuf[:padSize]); err != nil {
				return totalWritten, err
			}
			totalWritten += padSize
		}
	}

	return totalWritten, nil
}

// writeDataBlocks writes data blocks for flat-plain entries directly to out.
func (w *erofsWriter) writeDataBlocks(out io.Writer) error {
	for _, e := range w.entries {
		ds := w.flatPlainDataSize(e)
		if ds == 0 {
			continue
		}

		var n int
		switch e.mode & disk.StatTypeMask {
		case disk.StatTypeReg:
			expected := int64(ds)
			var written int64
			var err error
			limited := io.LimitReader(e.data, expected)
			// Use io.Copy for *os.File sources to enable copy_file_range.
			if _, ok := e.data.(*os.File); ok {
				written, err = io.Copy(out, limited)
			} else {
				written, err = io.CopyBuffer(onlyWriter{out}, limited, w.copyBuf)
			}
			if c, ok := e.data.(io.Closer); ok {
				_ = c.Close()
			}
			if err != nil {
				return fmt.Errorf("write data for %s: %w", e.path, err)
			}
			if written != expected {
				return fmt.Errorf("write data for %s: short read: got %d bytes, expected %d", e.path, written, expected)
			}
			n = int(written)
		case disk.StatTypeDir:
			written, err := w.writeDirents(out, e)
			if err != nil {
				return fmt.Errorf("write dirents for %s: %w", e.path, err)
			}
			n = written
		case disk.StatTypeSymlink:
			written, err := io.WriteString(out, e.symTarget)
			if err != nil {
				return fmt.Errorf("write symlink data for %s: %w", e.path, err)
			}
			n = written
		}

		if n%w.blockSize != 0 {
			padSize := w.blockSize - (n % w.blockSize)
			if _, err := out.Write(w.zeroBuf[:padSize]); err != nil {
				return fmt.Errorf("write padding for %s: %w", e.path, err)
			}
		}
	}
	return nil
}

// flatPlainDataSize returns the data size for a flat-plain entry, or 0.
func (w *erofsWriter) flatPlainDataSize(e *erofsEntry) int {
	if e.layout != disk.LayoutFlatPlain {
		return 0
	}
	switch e.mode & disk.StatTypeMask {
	case disk.StatTypeReg:
		if e.size > 0 && e.data != nil {
			return int(e.size)
		}
	case disk.StatTypeDir:
		return w.direntDataSize(e)
	case disk.StatTypeSymlink:
		return len(e.symTarget)
	}
	return 0
}

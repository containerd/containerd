package erofs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/erofs/go-erofs/internal/disk"
)

// Errors
var (
	// ErrInvalid occurs when an invalid value is detected in the erofs data.
	// Whether this invalid data is the result of corruption or bad input
	// is up to the caller to decide.
	// This error may be wrapped with more details.
	ErrInvalid = fs.ErrInvalid

	// ErrInvalidSuperblock occurs when the super block could not be validated
	// when initially loading the erofs input. Unlike other corruption cases,
	// invalid super block should be returned immediately
	ErrInvalidSuperblock = fmt.Errorf("invalid super block: %w", ErrInvalid)

	// ErrNotImplemented is returned when a feature is known but not implemented
	// yet by this library
	ErrNotImplemented = errors.New("not implemented")
)

// Stat is the erofs specific stat data returned by Stat and FileInfo requests
type Stat struct {
	Mode        fs.FileMode
	Size        int64
	InodeLayout uint8
	Rdev        uint32
	Inode       int64
	UID         uint32
	GID         uint32
	Mtime       uint64
	MtimeNs     uint32
	Nlink       int
	Xattrs      map[string]string
}

type options struct {
	extraDevices []io.ReaderAt
}

// Opt is an option for configuring the EroFS reader
type Opt func(*options)

// WithExtraDevices specifies additional devices to read
// chunk data from
func WithExtraDevices(devices ...io.ReaderAt) Opt {
	return func(o *options) {
		o.extraDevices = append(o.extraDevices, devices...)
	}
}

// EroFS returns a FileSystem reading from the given readerat.
// The readerat must be a valid erofs block file.
// No additional memory mapping is done and must be handled by
// the caller.
func EroFS(r io.ReaderAt, opts ...Opt) (fs.FS, error) {
	options := options{}
	for _, opt := range opts {
		opt(&options)
	}
	var superBlock [disk.SizeSuperBlock]byte
	n, err := r.ReadAt(superBlock[:], disk.SuperBlockOffset)
	if err != nil {
		return nil, err
	}

	if n != disk.SizeSuperBlock {
		return nil, fmt.Errorf("invalid super block: read %d bytes", n)
	}

	i := image{
		meta:         r,
		extraDevices: options.extraDevices,
	}
	if err = decodeSuperBlock(superBlock, &i.sb); err != nil {
		return nil, err
	}
	// TODO: check valid
	if i.sb.BlkSizeBits < 9 || i.sb.BlkSizeBits > 24 {
		return nil, fmt.Errorf("invalid super block: block size bits %d", i.sb.BlkSizeBits)
	}
	if int(i.sb.ExtraDevices) != len(options.extraDevices) {
		// TODO: Provide options for skipping extra devices and error out later?
		return nil, fmt.Errorf("invalid super block: extra devices count %d does not match provided %d", i.sb.ExtraDevices, len(options.extraDevices))
	}
	// Calculate device_id_mask
	// sbi->device_id_mask = roundup_pow_of_two(ondisk_extradevs + 1) - 1;
	totalDevs := uint32(i.sb.ExtraDevices) + 1
	if totalDevs == 0 {
		i.deviceIdMask = 0
	} else {
		totalDevs--
		totalDevs |= totalDevs >> 1
		totalDevs |= totalDevs >> 2
		totalDevs |= totalDevs >> 4
		totalDevs |= totalDevs >> 8
		totalDevs |= totalDevs >> 16
		totalDevs++
		i.deviceIdMask = uint16(totalDevs - 1)
	}

	i.blkPool.New = func() any {
		return &block{
			buf: make([]byte, 1<<i.sb.BlkSizeBits),
		}
	}

	return &i, nil
}

type image struct {
	sb disk.SuperBlock

	meta         io.ReaderAt
	extraDevices []io.ReaderAt
	deviceIdMask uint16
	blkPool      sync.Pool
	longPrefixes []string // cached long xattr prefixes
	prefixesOnce sync.Once
	prefixesErr  error
}

func (img *image) blkOffset() int64 {
	return int64(img.sb.MetaBlkAddr) << int64(img.sb.BlkSizeBits)
}

// loadLongPrefixes loads and caches the long xattr prefixes from the superblock
func (img *image) loadLongPrefixes() error {
	img.prefixesOnce.Do(func() {
		if img.sb.XattrPrefixCount == 0 {
			return
		}

		// Long prefixes are stored in the packed inode, after the inode header
		// The packed inode is typically a compact inode (32 bytes)
		// Address = MetaBlkAddr + (PackedNid * 32) + inode_size + (XattrPrefixStart * 4)
		// Note: XattrPrefixStart is in units of 4 bytes from the end of the packed inode
		baseAddr := img.blkOffset() + int64(img.sb.PackedNid)*32 + 32 + int64(img.sb.XattrPrefixStart)*4

		img.longPrefixes = make([]string, img.sb.XattrPrefixCount)

		// We'll read the prefixes incrementally since they can be large
		// and may span multiple blocks
		currentAddr := baseAddr
		var blk *block
		var buf []byte
		var err error
		bufOffset := 0 // offset within the current buffer

		for i := 0; i < int(img.sb.XattrPrefixCount); i++ {
			// Ensure we have at least 2 bytes to read the length
			if blk == nil || bufOffset+2 > len(buf) {
				if blk != nil {
					img.putBlock(blk)
				}
				blk, err = img.loadAt(currentAddr, int64(1<<img.sb.BlkSizeBits))
				if err != nil {
					img.prefixesErr = fmt.Errorf("failed to load long xattr prefix data at index %d: %w", i, err)
					return
				}
				buf = blk.bytes()
				bufOffset = 0
			}

			// Read length (little endian uint16) - includes base_index byte + infix bytes
			prefixLen := int(binary.LittleEndian.Uint16(buf[bufOffset : bufOffset+2]))
			bufOffset += 2
			currentAddr += 2

			if prefixLen < 1 {
				if blk != nil {
					img.putBlock(blk)
				}
				img.prefixesErr = fmt.Errorf("invalid long xattr prefix length %d at index %d", prefixLen, i)
				return
			}

			// Check if we have enough data for the prefix in current buffer
			if bufOffset+prefixLen > len(buf) {
				// Need to read more data - reload from current position
				if blk != nil {
					img.putBlock(blk)
				}
				// Load enough to cover this prefix
				loadSize := int64(prefixLen)
				if loadSize < int64(1<<img.sb.BlkSizeBits) {
					loadSize = int64(1 << img.sb.BlkSizeBits)
				}
				blk, err = img.loadAt(currentAddr, loadSize)
				if err != nil {
					img.prefixesErr = fmt.Errorf("failed to load long xattr prefix data for index %d: %w", i, err)
					return
				}
				buf = blk.bytes()
				bufOffset = 0

				// Verify we have enough now
				if prefixLen > len(buf) {
					img.putBlock(blk)
					img.prefixesErr = fmt.Errorf("long xattr prefix at index %d too large (%d bytes)", i, prefixLen)
					return
				}
			}

			// First byte is the base_index
			baseIndex := xattrIndex(buf[bufOffset])
			bufOffset++
			currentAddr++
			prefixLen--

			// Remaining bytes are the infix
			infix := string(buf[bufOffset : bufOffset+prefixLen])
			bufOffset += prefixLen
			currentAddr += int64(prefixLen)

			// Construct full prefix: base prefix + infix
			img.longPrefixes[i] = baseIndex.String() + infix

			// Align to 4-byte boundary
			// Total length field (2 bytes) + data (base_index + infix)
			totalLen := 2 + 1 + prefixLen
			if rem := totalLen % 4; rem != 0 {
				padding := 4 - rem
				bufOffset += padding
				currentAddr += int64(padding)
			}
		}

		if blk != nil {
			img.putBlock(blk)
		}
	})

	return img.prefixesErr
}

// getLongPrefix returns the long xattr prefix at the given index
func (img *image) getLongPrefix(index uint8) (string, error) {
	if err := img.loadLongPrefixes(); err != nil {
		return "", err
	}

	if int(index) >= len(img.longPrefixes) {
		return "", fmt.Errorf("long xattr prefix index %d out of range (max %d)", index, len(img.longPrefixes)-1)
	}

	return img.longPrefixes[index], nil
}

func (img *image) loadAt(addr, size int64) (*block, error) {
	blkSize := int64(1 << img.sb.BlkSizeBits)
	if size > blkSize {
		size = blkSize
	}

	b := img.getBlock()
	n, err := img.meta.ReadAt(b.buf[:size], addr)
	if err != nil {
		if err == io.EOF && n > 0 {
			// Hit EOF but read some data
		} else {
			img.putBlock(b)
			return nil, fmt.Errorf("failed to read %d bytes at %d: %w", size, addr, err)
		}
	}
	b.offset = 0
	b.end = int32(n)

	return b, nil
}

// loadBlock loads the block with the given data
func (img *image) loadBlock(fi *fileInfo, pos int64) (*block, error) {
	nblocks := calculateBlocks(img.sb.BlkSizeBits, fi.size)
	bn := int(pos >> int(img.sb.BlkSizeBits))
	if bn >= nblocks {
		return nil, fmt.Errorf("block position larger than number of blocks for inode: %w", io.EOF)
	}
	var addr int64
	blockSize := int(1 << img.sb.BlkSizeBits)
	blockOffset := 0
	blockEnd := blockSize
	switch fi.inodeLayout {
	case disk.LayoutFlatPlain:
		// flat plain has no holes
		addr = int64(int(fi.inodeData)+bn) << img.sb.BlkSizeBits
		if bn == nblocks-1 {
			blockEnd = int(fi.size - int64(bn)*int64(1<<img.sb.BlkSizeBits))
		}
	case disk.LayoutFlatInline:
		// If on the last block, validate
		if bn == nblocks-1 {
			addr = img.blkOffset() + int64(fi.inode*disk.SizeInodeCompact)
			// Move to the data offset from the start of the inode
			addr += fi.dataOffset()

			// Get the ooffset from the start of the block
			blockOffset = int(addr & int64(blockSize-1))
			// Calculate end of block using data offset + tail data size
			blockEnd = int(fi.size-int64(bn*blockSize)) + blockOffset

			// Ensure the last block is not exceeded
			if blockEnd > blockSize {
				return nil, fmt.Errorf("inline data cross block boundary for nid %d: %w", fi.inode, ErrInvalid)
			}
			// Move the offset within the block based on position within file
			blockOffset += int(pos - int64(bn<<int(img.sb.BlkSizeBits)))
		} else {
			addr = int64(int(fi.inodeData)+bn) << img.sb.BlkSizeBits
		}
	case disk.LayoutChunkBased:
		// first 2 le bytes for format, second 2 bytes are reserved
		format := uint16(fi.inodeData)
		if format&^(disk.LayoutChunkFormatBits|disk.LayoutChunkFormatIndexes|disk.LayoutChunkFormat48Bit) != 0 {
			return nil, fmt.Errorf("unsupported chunk format %x for nid %d: %w", format, fi.inode, ErrInvalid)
		}

		chunkbits := img.sb.BlkSizeBits + uint8(format&disk.LayoutChunkFormatBits)
		chunkn := int((fi.size-1)>>chunkbits) + 1
		cn := int(pos >> chunkbits)

		if cn >= chunkn {
			return nil, fmt.Errorf("chunk format does not fit into allocated bytes for nid %d: %w", fi.inode, ErrInvalid)
		}

		inodeStart := img.blkOffset() + int64(fi.inode*disk.SizeInodeCompact)
		baseOffset := inodeStart + fi.dataOffset()

		unit := int64(4)
		if format&disk.LayoutChunkFormatIndexes == disk.LayoutChunkFormatIndexes {
			unit = 8
			// Align to 8 bytes
			if baseOffset%8 != 0 {
				baseOffset = (baseOffset + 7) & ^int64(7)
			}
		}

		entryPos := baseOffset + int64(cn)*unit
		entryBuf := make([]byte, unit)
		if _, err := img.meta.ReadAt(entryBuf, entryPos); err != nil {
			return nil, fmt.Errorf("failed to read chunk entry at %d: %w", entryPos, err)
		}

		var addr int64
		var deviceID uint16

		if unit == 8 {
			var idx disk.InodeChunkIndex
			if err := binary.Read(bytes.NewReader(entryBuf), binary.LittleEndian, &idx); err != nil {
				return nil, err
			}

			var addrmask uint64
			if format&disk.LayoutChunkFormat48Bit == disk.LayoutChunkFormat48Bit {
				addrmask = (1 << 48) - 1
			} else {
				addrmask = (1 << 32) - 1
			}

			startblk := (uint64(idx.StartBlkHi) << 32) | uint64(idx.StartBlkLo)
			startblk &= addrmask

			if (startblk^0xFFFFFFFFFFFFFFFF)&addrmask == 0 {
				addr = -1
			} else {
				addr = int64(startblk) << img.sb.BlkSizeBits
				deviceID = idx.DeviceId & img.deviceIdMask
			}
		} else {
			var rawAddr uint32
			if err := binary.Read(bytes.NewReader(entryBuf), binary.LittleEndian, &rawAddr); err != nil {
				return nil, err
			}
			if rawAddr == 0xFFFFFFFF {
				addr = -1
			} else {
				addr = int64(rawAddr) << img.sb.BlkSizeBits
			}
		}

		if bn == nblocks-1 {
			blockEnd = int(fi.size - int64(bn)*int64(1<<img.sb.BlkSizeBits))
		}

		if addr == -1 {
			// Null address, return new zero filled block
			return &block{
				buf: make([]byte, 1<<img.sb.BlkSizeBits),
				end: int32(blockEnd),
			}, nil
		}

		// Add block offset within chunk
		blockPos := int64(bn) << img.sb.BlkSizeBits
		if blockPos > 0 {
			addr += (blockPos - int64(cn<<chunkbits))
		}

		reader := img.meta
		if deviceID > 0 {
			if int(deviceID) > len(img.extraDevices) {
				return nil, fmt.Errorf("invalid device id %d", deviceID)
			}
			reader = img.extraDevices[deviceID-1]
		}

		b := img.getBlock()
		if n, err := reader.ReadAt(b.buf[blockOffset:blockEnd], addr); err != nil {
			return nil, fmt.Errorf("failed to read block for nid %d: %w", fi.inode, err)
		} else if n != (blockEnd - blockOffset) {
			return nil, fmt.Errorf("failed to read full block for nid %d: %w", fi.inode, ErrInvalid)
		}
		b.offset = int32(blockOffset)
		b.end = int32(blockEnd)
		return b, nil
	case disk.LayoutCompressedFull, disk.LayoutCompressedCompact:
		return nil, fmt.Errorf("inode layout (%d) for %d: %w", fi.inodeLayout, fi.inode, ErrNotImplemented)
	default:
		return nil, fmt.Errorf("inode layout (%d) for %d: %w", fi.inodeLayout, fi.inode, ErrInvalid)
	}
	if blockOffset >= blockEnd {
		return nil, fmt.Errorf("no remaining items in block: %w", io.EOF)
	}

	b := img.getBlock()
	if n, err := img.meta.ReadAt(b.buf[blockOffset:blockEnd], addr); err != nil {
		return nil, fmt.Errorf("failed to read block for nid %d: %w", fi.inode, err)
	} else if n != (blockEnd - blockOffset) {
		return nil, fmt.Errorf("failed to read full block for nid %d: %w", fi.inode, ErrInvalid)
	}
	b.offset = int32(blockOffset)
	b.end = int32(blockEnd)

	return b, nil
}

func (img *image) getBlock() *block {
	return img.blkPool.Get().(*block)
}

// putBlock returns a block after complete so its
// buffer can be put back into the buffer pool
func (img *image) putBlock(b *block) {
	img.blkPool.Put(b)
}

func (i *image) dirEntry(nid uint64, name string) (uint64, fs.FileMode, error) {
	return 0, 0, errors.New("direntry: not implemented")
}

func (i *image) Open(name string) (fs.File, error) {
	var err error
	original := name
	if filepath.IsAbs(name) {
		name, err = filepath.Rel("/", name)
		if err != nil {
			return nil, err
		}
	} else {
		name = filepath.Clean(name)
	}
	if name == "." {
		name = ""
	}

	nid := uint64(i.sb.RootNid)
	ftype := fs.ModeDir

	parent := "/"
	basename := name
	for name != "" {
		var sep int
		for sep < len(name) && !os.IsPathSeparator(name[sep]) {
			sep++
		}
		if sep < len(name) {
			basename = name[:sep]
			name = name[sep+1:]
		} else {
			basename = name
			name = ""
		}

		if ftype != fs.ModeDir {
			// TODO: Path error
			return nil, errors.New("not a directory")
		}
		dir := &dir{
			file: file{
				img:   i,
				name:  parent,
				inode: nid,
				ftype: ftype,
			},
		}
		// TODO: Lookup in directory instead of reading all
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return nil, fmt.Errorf("failed to read dir: %w", err)
		}
		var found bool
		for _, e := range entries {
			if e.Name() == basename {
				nid = uint64(e.(*direntry).file.inode)
				ftype = e.(*direntry).file.ftype & fs.ModeType
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s not found: %w", original, fs.ErrNotExist)
		}
		parent = basename
	}

	if basename == "" {
		basename = original
	}

	b := file{
		img:   i,
		name:  basename,
		inode: nid,
		ftype: ftype,
	}
	if ftype.IsDir() {
		return &dir{file: b}, nil
	}

	return &b, nil
}

type file struct {
	img   *image
	name  string
	inode uint64
	ftype fs.FileMode

	// Mutable fields, open file should not be accessed concurrently
	offset int64     // current offset for read operations
	info   *fileInfo // cached fileInfo
}

func (b *file) readInfo(infoOnly bool) (fi *fileInfo, err error) {
	if b.info != nil {
		return b.info, nil
	}

	addr := b.img.blkOffset() + int64(b.inode*disk.SizeInodeCompact)
	blkSize := int32(1 << b.img.sb.BlkSizeBits)
	blk := b.img.getBlock()
	blk.offset = int32(addr & int64(blkSize-1))
	blk.end = blkSize
	if blk.end-blk.offset < disk.SizeInodeExtended {
		// Use buffer starting from beginning of inode, do not use the position
		// in the block since an extended inode may span multiple blocks
		blk.offset = 0
		blk.end = disk.SizeInodeExtended
	}
	ino := blk.bytes()
	_, err = b.img.meta.ReadAt(ino, addr)
	if err != nil {
		return nil, err
	}

	defer func() {
		v := recover()
		if v != nil {
			err = fmt.Errorf("file format error: %v", v)
		}

	}()

	var format uint16
	if _, err := binary.Decode(ino[:2], binary.LittleEndian, &format); err != nil {
		return nil, err
	}

	layout := uint8((format & 0x0E) >> 1)
	if format&0x01 == 0 {
		var inode disk.InodeCompact
		if _, err := binary.Decode(ino[:disk.SizeInodeCompact], binary.LittleEndian, &inode); err != nil {
			return nil, err
		}
		b.info = &fileInfo{
			name:        b.name,
			inode:       b.inode,
			isize:       disk.SizeInodeCompact,
			inodeLayout: layout,
			inodeData:   inode.InodeData,
			size:        int64(inode.Size),
			mode:        (fs.FileMode(inode.Mode) & ^fs.ModeType) | b.ftype,
			//modTime: time.Unix(int64(inode.Mtime), int64(inode.MtimeNs)),
			// TODO: Set mtime to zero value?
		}
		if inode.XattrCount > 0 {
			b.info.xsize = int(inode.XattrCount-1)*disk.SizeXattrEntry + disk.SizeXattrBodyHeader
		}
		if infoOnly {
			b.info.stat = &Stat{
				Mode:        disk.EroFSModeToGoFileMode(inode.Mode),
				Size:        int64(inode.Size),
				InodeLayout: layout,
				Inode:       int64(inode.Inode),
				Rdev:        disk.RdevFromMode(inode.Mode, inode.InodeData),
				UID:         uint32(inode.UID),
				GID:         uint32(inode.GID),
				Nlink:       int(inode.Nlink),
				//Mtime        uint64
				//MtimeNs      uint32
			}
		}
		addr += disk.SizeInodeCompact
	} else {
		var inode disk.InodeExtended
		if _, err := binary.Decode(ino[:disk.SizeInodeExtended], binary.LittleEndian, &inode); err != nil {
			return nil, err
		}
		b.info = &fileInfo{
			name:        b.name,
			inode:       b.inode,
			isize:       disk.SizeInodeExtended,
			inodeLayout: layout,
			inodeData:   inode.InodeData,
			size:        int64(inode.Size),
			mode:        (fs.FileMode(inode.Mode) & ^fs.ModeType) | b.ftype,
			modTime:     time.Unix(int64(inode.Mtime), int64(inode.MtimeNs)),
		}
		if inode.XattrCount > 0 {
			b.info.xsize = int(inode.XattrCount-1)*disk.SizeXattrEntry + disk.SizeXattrBodyHeader
		}
		if infoOnly {
			b.info.stat = &Stat{
				Mode:        disk.EroFSModeToGoFileMode(inode.Mode),
				Size:        int64(inode.Size),
				InodeLayout: layout,
				Inode:       int64(inode.Inode),
				Rdev:        disk.RdevFromMode(inode.Mode, inode.InodeData),
				UID:         uint32(inode.UID),
				GID:         uint32(inode.GID),
				Nlink:       int(inode.Nlink),
				Mtime:       inode.Mtime,
				MtimeNs:     inode.MtimeNs,
			}
		}
		addr += disk.SizeInodeExtended
	}

	if infoOnly && b.info.xsize > 0 {
		if err := setXattrs(b, addr, blk); err != nil {
			return nil, err
		}
	} else if infoOnly || b.info.inodeLayout == disk.LayoutFlatPlain || b.info.size == 0 || blk.end != blkSize {
		b.img.putBlock(blk)
	} else {
		// If the inode has trailing data used later, cache it
		b.info.cached = blk
	}
	return b.info, nil
}

func (b *file) Stat() (fs.FileInfo, error) {
	return b.readInfo(true)
}

func (b *file) Read(p []byte) (int, error) {
	fi, err := b.readInfo(false)
	if err != nil {
		return 0, err
	}

	var n int
	for len(p) > 0 {
		if b.offset >= fi.size {
			return n, io.EOF
		}
		blk, err := b.img.loadBlock(fi, b.offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.EOF
				b.offset += int64(n)
			}
			return n, err
		}
		buf := blk.bytes()
		copied := copy(p, buf)
		n += copied
		p = p[copied:]
		b.offset += int64(copied)

		b.img.putBlock(blk)
	}
	return n, nil
}

func (b *file) Close() error {
	b.info.cached = nil
	return nil
}

type direntry struct {
	file
}

func (d *direntry) Name() string {
	return d.name
}

func (d *direntry) IsDir() bool {
	return d.ftype.IsDir()
}

func (d *direntry) Type() fs.FileMode {
	return d.ftype
}

func (d *direntry) Info() (fs.FileInfo, error) {
	return d.readInfo(true)
}

type dir struct {
	file

	//bn is the current block to read from (relative to file start)
	bn int

	//consumed is how many have been returned in the current block
	consumed uint16
}

func (d *dir) ReadDir(n int) ([]fs.DirEntry, error) {
	fi, err := d.readInfo(false)
	if err != nil {
		return nil, fmt.Errorf("readInfo failed: %w", err)
	}

	var ents []fs.DirEntry
	pos := int64(d.bn << d.img.sb.BlkSizeBits)
	for pos < fi.size {
		b, err := d.img.loadBlock(fi, pos)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ents, nil
			}
			return nil, err
		}
		buf := b.bytes()
		if len(buf) < 12 {
			return ents, nil
		}

		var dirents [2]disk.Dirent

		readN, err := binary.Decode(buf[:12], binary.LittleEndian, &dirents[0])
		if err != nil {
			return nil, fmt.Errorf("decode failed: %w", err)
		}
		if readN != 12 {
			return nil, errors.New("invalid dirent: not fully decoded")
		}

		entryN := dirents[0].NameOff / disk.SizeDirent

		for i := uint16(0); i < entryN; i++ {
			var name string
			if i < entryN-1 {
				start := 12 * (i + 1)
				readN, err := binary.Decode(buf[start:start+12], binary.LittleEndian, &dirents[1])
				if err != nil {
					return nil, fmt.Errorf("decode failed: %w", err)
				}
				if readN != 12 {
					return nil, errors.New("invalid dirent: not fully decoded")
				}
				name = string(buf[dirents[0].NameOff:dirents[1].NameOff])
			} else {
				name = string(buf[dirents[0].NameOff:])
			}

			if i >= d.consumed && name != "." && name != ".." {
				b := file{
					img:   d.file.img,
					name:  name,
					inode: dirents[0].Nid,
					ftype: disk.EroFSFtypeToFileMode(dirents[0].FileType),
				}
				ents = append(ents, &direntry{b})
				d.consumed = i + 1

				if n > 0 && len(ents) == n {
					if i == entryN-1 {
						d.consumed = 0
						d.bn++
					}
					return ents, nil
				}
			}

			// Rotate next to current
			dirents[0] = dirents[1]
		}

		d.consumed = 0
		d.bn++
		pos = int64(d.bn << d.img.sb.BlkSizeBits)
	}

	return ents, nil
}

type fileInfo struct {
	name        string
	inode       uint64
	isize       int8
	xsize       int
	inodeLayout uint8
	inodeData   uint32
	size        int64
	mode        fs.FileMode
	modTime     time.Time
	stat        *Stat
	cached      *block
}

func (fi *fileInfo) Name() string {
	return fi.name
}

func (fi *fileInfo) Size() int64 {
	return fi.size
}

func (fi *fileInfo) Mode() fs.FileMode {
	return fi.mode
}
func (fi *fileInfo) ModTime() time.Time {
	return fi.modTime
}

func (fi *fileInfo) IsDir() bool {
	return fi.mode.IsDir()
}

func (fi *fileInfo) Sys() any {
	// Return erofs stat object with extra fields and call for xattrs
	return fi.stat
}

func (fi *fileInfo) dataOffset() int64 {
	// inode size + xattr size
	return int64(fi.isize) + int64(fi.xsize)
}
func decodeSuperBlock(b [disk.SizeSuperBlock]byte, sb *disk.SuperBlock) error {
	n, err := binary.Decode(b[:], binary.LittleEndian, sb)
	if err != nil {
		return err
	}
	if n != disk.SizeSuperBlock {
		return fmt.Errorf("invalid super block: decoded %d bytes", n)
	}
	if sb.MagicNumber != disk.MagicNumber {
		return fmt.Errorf("invalid super block: invalid magic number %x", sb.MagicNumber)
	}
	return nil
}

package erofs

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
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

	// ErrNotDirectory is returned when a path component is not a directory.
	ErrNotDirectory = errors.New("not a directory")

	// ErrIsDirectory is returned when an operation expected a file but found
	// a directory.
	ErrIsDirectory = errors.New("is a directory")

	// ErrLoop is returned when too many symlinks are encountered during
	// path resolution.
	ErrLoop = fmt.Errorf("too many symlinks: %w", ErrInvalid)
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

// OpenOpt is an option for configuring the EROFS reader
type OpenOpt func(*options)

// Deprecated: Use [OpenOpt] instead, will be removed in 0.3
type Opt = OpenOpt

// WithExtraDevices specifies additional devices to read
// chunk data from
func WithExtraDevices(devices ...io.ReaderAt) OpenOpt {
	return func(o *options) {
		o.extraDevices = append(o.extraDevices, devices...)
	}
}

// Open returns a FileSystem reading from the given ReaderAt.
// The ReaderAt must be a valid EROFS block file.
// No additional memory mapping is done and must be handled by
// the caller.
func Open(r io.ReaderAt, opts ...OpenOpt) (fs.FS, error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
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
		meta: r,
	}
	if err = decodeSuperBlock(superBlock, &i.sb); err != nil {
		return nil, err
	}
	// The maximum reasonable filesystem block size is 64k, which is
	// the largest supported page size of aarch64 platforms.
	if i.sb.BlkSizeBits < 9 || i.sb.BlkSizeBits > 16 {
		return nil, fmt.Errorf("unsupported block size bits %d: %w", i.sb.BlkSizeBits, ErrInvalidSuperblock)
	}
	unknownFeat := i.sb.FeatureIncompat &^ disk.FeatureIncompatAll
	if unknownFeat != 0 {
		return nil, fmt.Errorf("unsupported incompatible feature 0x%x: %w", unknownFeat, ErrNotImplemented)
	}
	ondiskExtraDevices := uint32(0)
	if i.sb.FeatureIncompat&disk.FeatureIncompatDeviceTable != 0 {
		ondiskExtraDevices = uint32(i.sb.ExtraDevices)
		// Calculate device_id_mask
		// sbi->device_id_mask = roundup_pow_of_two(ondisk_extradevs + 1) - 1;
		i.deviceIDMask = uint16(roundupPowerOfTwo(uint32(i.sb.ExtraDevices)+1) - 1)
	}

	if int(ondiskExtraDevices) != len(o.extraDevices) {
		// TODO: Provide options for skipping extra devices and error out later?
		return nil, fmt.Errorf("invalid super block: extra devices count %d does not match provided %d", ondiskExtraDevices, len(o.extraDevices))
	}

	// Parse the device table if extra devices exist
	if ondiskExtraDevices > 0 {
		devTableOffset := int64(i.sb.DevtSlotOff) * disk.SizeDeviceSlot
		i.devices = make([]deviceInfo, int(ondiskExtraDevices))
		for idx := range i.devices {
			var slotBuf [disk.SizeDeviceSlot]byte
			offset := devTableOffset + int64(idx)*disk.SizeDeviceSlot
			if _, err := r.ReadAt(slotBuf[:], offset); err != nil {
				return nil, fmt.Errorf("failed to read device slot %d at offset %d: %w", idx, offset, err)
			}
			var slot disk.DeviceSlot
			if _, err := binary.Decode(slotBuf[:], binary.LittleEndian, &slot); err != nil {
				return nil, fmt.Errorf("failed to decode device slot %d: %w", idx, err)
			}
			i.devices[idx] = deviceInfo{
				device:        o.extraDevices[idx],
				mappedBlkAddr: slot.MappedBlkAddr,
				blocks:        slot.Blocks,
			}
		}
	}

	// Error out filesystems with unsupported compressed inodes
	if i.sb.FeatureIncompat&disk.FeatureIncompatLZ4_0Padding != 0 ||
		i.sb.ComprAlgs != 0 {
		return nil, fmt.Errorf("unsupported compressed filesystem (FeatureIncompat=0x%x, ComprAlgs=0x%x): %w",
			i.sb.FeatureIncompat, i.sb.ComprAlgs, ErrNotImplemented)
	}

	i.blkPool.New = func() any {
		return &block{
			buf: make([]byte, 1<<i.sb.BlkSizeBits),
		}
	}

	return &i, nil
}

// Deprecated: Use [Open] instead, will be removed in 0.3
func EroFS(r io.ReaderAt, opts ...Opt) (fs.FS, error) {
	return Open(r, opts...)
}

// roundupPowerOfTwo rounds v up to the next power of two.
func roundupPowerOfTwo(v uint32) uint32 {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

// deviceInfo holds the parsed mapped address range for a device table entry.
type deviceInfo struct {
	device        io.ReaderAt
	mappedBlkAddr uint32 // starting mapped block address
	blocks        uint32 // total block count for this device
}

type image struct {
	sb disk.SuperBlock

	meta         io.ReaderAt
	devices      []deviceInfo // parsed device table entries
	deviceIDMask uint16
	blkPool      sync.Pool
	longPrefixes []string // cached long xattr prefixes
	prefixesOnce sync.Once
	prefixesErr  error
}

// start physical offset of the separate metadata zone
func (img *image) metaStartPos() int64 {
	return int64(img.sb.MetaBlkAddr) << int64(img.sb.BlkSizeBits)
}

// maxReadFileSize is the maximum file size that ReadFile will allocate.
// ReadFile is intended for small files; for larger files, callers should
// use Open and io.Copy. 128 MiB is generous for typical use (configs,
// manifests, symlink targets, etc.) while guarding against
// unexpectedly large files.
const maxReadFileSize = 128 << 20 // 128 MiB

// mapDev resolves map->m_bdev and map->m_pa mapping for go-erofs.
// It works similarly to erofs_map_dev in the linux kernel.
func (img *image) mapDev(deviceID uint16, pa int64) (io.ReaderAt, int64, error) {
	if deviceID > 0 {
		if int(deviceID) > len(img.devices) {
			return nil, 0, fmt.Errorf("invalid device id %d", deviceID)
		}
		return img.devices[deviceID-1].device, pa, nil
	}

	if len(img.devices) > 0 {
		for _, dev := range img.devices {
			if dev.mappedBlkAddr == 0 {
				continue
			}

			startOff := int64(dev.mappedBlkAddr) << img.sb.BlkSizeBits
			length := int64(dev.blocks) << img.sb.BlkSizeBits

			if pa >= startOff && pa < startOff+length {
				return dev.device, pa - startOff, nil
			}
		}
	}

	return img.meta, pa, nil
}

func (img *image) readMetadata(r io.Reader) ([]byte, error) {
	// - A 2-byte little-endian length field, which is aligned to a 4-byte boundary
	// - The length bytes of payload data
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to read metadata length %v: %w", lenBuf, err)
	}

	dataLen := int(binary.LittleEndian.Uint16(lenBuf[:]))
	if dataLen < 1 {
		dataLen = 65536
	}

	data := make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("failed to read metadata payload: %w", err)
	}

	// Align to 4-byte boundary except for hitting EOF
	totalLen := 2 + dataLen
	if rem := totalLen % 4; rem != 0 {
		padding := int64(4 - rem)
		if _, err := io.CopyN(io.Discard, r, padding); err != nil &&
			!errors.Is(err, io.EOF) &&
			!errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("failed to discard padding of %d bytes: %w", padding, err)
		}
	}
	return data, nil
}

// loadLongPrefixes loads and caches the long xattr prefixes from the packed inode
// using the regular inode read logic to handle compressed/non-inline data.
//
// Long xattr name prefixes are used to optimize storage of xattrs with common
// prefixes. They are stored sequentially in a special "packed inode" or
// "meta inode".
// See: https://docs.kernel.org/filesystems/erofs.html#extended-attributes
func (img *image) loadLongPrefixes() error {
	img.prefixesOnce.Do(func() {
		if img.sb.XattrPrefixCount == 0 {
			return
		}

		var r io.Reader

		// Calculate the starting offset. XattrPrefixStart is defined in the
		// superblock as being in units of 4 bytes from the start of the corresponding inode
		startOffset := int64(img.sb.XattrPrefixStart) * 4

		if (img.sb.FeatureIncompat&disk.FeatureIncompatFragments != 0) && img.sb.PackedNid > 0 {
			// The packed inode (identified by PackedNid in the superblock) is a special
			// inode used for shared data and metadata.
			// We use ".packed" as a descriptive name for this internal inode.
			f := &file{
				img:   img,
				name:  ".packed",
				nid:   img.sb.PackedNid,
				ftype: 0, // regular file
			}

			// Read inode info to determine size and layout
			fi, err := f.readInfo(false)
			if err != nil {
				img.prefixesErr = fmt.Errorf("failed to read packed inode: %w", err)
				return
			}

			if startOffset > fi.size {
				img.prefixesErr = fmt.Errorf("xattr prefix start offset %d exceeds packed inode size %d", startOffset, fi.size)
				return
			}

			// Set the read offset
			f.offset = startOffset
			r = bufio.NewReader(f)
		} else {
			// FIXME(hsiangkao): should avoid hacky 1<<32 here since we don't care about the end
			r = io.NewSectionReader(img.meta, startOffset, 1<<32)
		}

		img.longPrefixes = make([]string, img.sb.XattrPrefixCount)
		for i := 0; i < int(img.sb.XattrPrefixCount); i++ {
			data, err := img.readMetadata(r)
			if err != nil {
				img.prefixesErr =
					fmt.Errorf("failed to read long xattr prefix %d: %w", i, err)
				return
			}

			// First byte is the base_index referencing a standard xattr prefix
			baseIndex := xattrIndex(data[0])

			// Remaining bytes are the infix to be appended to the base prefix
			infix := string(data[1:])

			// Construct full prefix: base prefix + infix
			img.longPrefixes[i] = baseIndex.String() + infix
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
	if n, err := img.meta.ReadAt(b.buf[:size], addr); err != nil {
		img.putBlock(b)
		return nil, fmt.Errorf("failed to read %d bytes at %d: %w", size, addr, err)
	} else {
		b.offset = 0
		b.end = int32(n)
	}

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
		blockOffset = int(pos % int64(blockSize))
		if bn == nblocks-1 {
			blockEnd = int(fi.size - int64(bn)*int64(1<<img.sb.BlkSizeBits))
		}
	case disk.LayoutFlatInline:
		// If on the last block, validate
		if bn == nblocks-1 {
			addr = img.metaStartPos() + int64(fi.nid*disk.SizeInodeCompact)
			// Move to the data offset from the start of the inode
			addr += fi.flatDataOffset()

			// Get the ooffset from the start of the block
			blockOffset = int(addr & int64(blockSize-1))

			// Move addr to start of block
			addr = (addr & ^int64(blockSize-1))

			// Move the offset within the block based on position within file
			blockOffset += int(pos - int64(bn<<int(img.sb.BlkSizeBits)))
			blockEnd = int(fi.size-int64(bn*blockSize)) + blockOffset

			// Ensure the last block is not exceeded
			if blockEnd > blockSize {
				return nil, fmt.Errorf("inline data cross block boundary for nid %d: %w", fi.nid, ErrInvalid)
			}
		} else {
			addr = int64(int(fi.inodeData)+bn) << img.sb.BlkSizeBits
			blockOffset = int(pos % int64(blockSize))
		}
	case disk.LayoutChunkBased:
		// first 2 le bytes for format, second 2 bytes are reserved
		format := uint16(fi.inodeData)
		if format&disk.LayoutChunkFormat48Bit != 0 {
			return nil, fmt.Errorf("48-bit chunk format for nid %d: %w", fi.nid, ErrNotImplemented)
		}
		if format&^(disk.LayoutChunkFormatBits|disk.LayoutChunkFormatIndexes) != 0 {
			return nil, fmt.Errorf("unsupported chunk format %x for nid %d: %w", format, fi.nid, ErrInvalid)
		}

		chunkbits := img.sb.BlkSizeBits + uint8(format&disk.LayoutChunkFormatBits)
		chunkn := int((fi.size-1)>>chunkbits) + 1
		cn := int(pos >> chunkbits)

		if cn >= chunkn {
			return nil, fmt.Errorf("chunk format does not fit into allocated bytes for nid %d: %w", fi.nid, ErrInvalid)
		}

		inodeStart := img.metaStartPos() + int64(fi.nid*disk.SizeInodeCompact)
		baseOffset := inodeStart + fi.flatDataOffset()

		unit := 4
		if format&disk.LayoutChunkFormatIndexes == disk.LayoutChunkFormatIndexes {
			unit = 8
			// Align to 8 bytes
			if baseOffset%8 != 0 {
				baseOffset = (baseOffset + 7) & ^int64(7)
			}
		}

		entryPos := baseOffset + int64(cn*unit)
		var entryBuf [8]byte
		if n, err := img.meta.ReadAt(entryBuf[:unit], entryPos); err != nil {
			return nil, fmt.Errorf("failed to read chunk entry at %d: %w", entryPos, err)
		} else if n != unit {
			return nil, fmt.Errorf("short read of chunk entry at %d: read %d bytes, expected %d", entryPos, n, unit)
		}

		var addr int64
		var deviceID uint16

		if unit == 8 {
			startBlkLo := binary.LittleEndian.Uint32(entryBuf[4:8])
			if ^startBlkLo == 0 {
				addr = -1
			} else {
				addr = int64(startBlkLo) << img.sb.BlkSizeBits
				deviceID = binary.LittleEndian.Uint16(entryBuf[2:4]) & img.deviceIDMask
			}
		} else {
			rawAddr := binary.LittleEndian.Uint32(entryBuf[:4])
			if ^rawAddr == 0 {
				addr = -1
			} else {
				addr = int64(rawAddr) << img.sb.BlkSizeBits
			}
		}

		if bn == nblocks-1 {
			blockEnd = int(fi.size - int64(bn)*int64(1<<img.sb.BlkSizeBits))
		}
		blockOffset = int(pos % int64(blockSize))

		if addr == -1 {
			// Null address, return new zero filled block
			return &block{
				buf:    make([]byte, 1<<img.sb.BlkSizeBits),
				offset: int32(blockOffset),
				end:    int32(blockEnd),
			}, nil
		}

		// Add block offset within chunk
		blockPos := int64(bn) << img.sb.BlkSizeBits
		if blockPos > 0 {
			addr += (blockPos - int64(cn<<chunkbits))
		}

		reader, mappedAddr, err := img.mapDev(deviceID, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to map device for nid %d: %w", fi.nid, err)
		}
		addr = mappedAddr

		b := img.getBlock()
		if n, err := reader.ReadAt(b.buf[blockOffset:blockEnd], addr+int64(blockOffset)); err != nil {
			img.putBlock(b)
			return nil, fmt.Errorf("failed to read block for nid %d: %w", fi.nid, err)
		} else if n != (blockEnd - blockOffset) {
			img.putBlock(b)
			return nil, fmt.Errorf("failed to read full block for nid %d: %w", fi.nid, ErrInvalid)
		}
		b.offset = int32(blockOffset)
		b.end = int32(blockEnd)
		return b, nil
	case disk.LayoutCompressedFull, disk.LayoutCompressedCompact:
		return nil, fmt.Errorf("inode layout (%d) for %d: %w", fi.inodeLayout, fi.nid, ErrNotImplemented)
	default:
		return nil, fmt.Errorf("inode layout (%d) for %d: %w", fi.inodeLayout, fi.nid, ErrInvalid)
	}
	if blockOffset >= blockEnd {
		return nil, fmt.Errorf("no remaining items in block: %w", io.EOF)
	}

	b := img.getBlock()
	b.offset = int32(blockOffset)
	b.end = int32(blockEnd)
	if n, err := img.meta.ReadAt(b.bytes(), addr+int64(blockOffset)); err != nil {
		img.putBlock(b)
		return nil, fmt.Errorf("failed to read block for nid %d: %w", fi.nid, err)
	} else if n != blockEnd-blockOffset {
		img.putBlock(b)
		return nil, fmt.Errorf("failed to read full block for nid %d: %w, expected %d, actual %d", fi.nid, ErrInvalid, blockEnd-blockOffset, n)
	}
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

const maxSymlinks = 255

// maxSymlinkSize is the maximum size of a symlink target.
// Linux PATH_MAX is 4096; we use the same limit.
const maxSymlinkSize = 4096

// readLink reads the symlink target for the given nid.
func (i *image) readLink(nid uint64, name string) (string, error) {
	f := &file{img: i, name: name, nid: nid, ftype: fs.ModeSymlink}
	fi, err := f.readInfo(false)
	if err != nil {
		return "", err
	}
	if fi.size < 0 || fi.size > maxSymlinkSize {
		return "", fmt.Errorf("symlink target size %d out of range: %w", fi.size, ErrInvalid)
	}
	buf := make([]byte, fi.size)
	if fi.size > 0 {
		if _, err = f.Read(buf); err != nil && err != io.EOF {
			return "", err
		}
	}
	return string(buf), nil
}

// resolve cleans the path and walks directory entries to find the target inode.
// When follow is true, symlinks are followed (including the final component).
// When follow is false, the final component is not followed (for Lstat/ReadLink).
// Intermediate symlinks are always followed.
func (i *image) resolve(op, name string, follow bool) (nid uint64, ftype fs.FileMode, basename string, err error) {
	original := name
	if path.IsAbs(name) {
		name = name[1:]
	}
	name = path.Clean(name)
	if name == "." {
		name = ""
	}

	nid = uint64(i.sb.RootNid)
	ftype = fs.ModeDir

	// curPath tracks the full resolved path of the current directory
	// so that relative symlink targets can be resolved correctly.
	linksFollowed := 0
	curPath := ""
	basename = name
	for name != "" {
		var sep int
		for sep < len(name) && name[sep] != '/' {
			sep++
		}
		var rest string
		if sep < len(name) {
			basename = name[:sep]
			rest = name[sep+1:]
		} else {
			basename = name
			rest = ""
		}

		if ftype != fs.ModeDir {
			return 0, 0, "", &fs.PathError{Op: op, Path: original, Err: ErrNotDirectory}
		}
		d := &dir{
			file: file{
				img:   i,
				name:  basename,
				nid:   nid,
				ftype: ftype,
			},
		}
		entNid, entFtype, err := d.lookup(basename)
		if err != nil {
			return 0, 0, "", &fs.PathError{Op: op, Path: original, Err: err}
		}
		nid = entNid
		ftype = entFtype & fs.ModeType

		// Follow symlinks for intermediate components always,
		// and for the final component only when follow is true.
		isFinal := rest == ""
		if ftype&fs.ModeSymlink != 0 && (follow || !isFinal) {
			linksFollowed++
			if linksFollowed > maxSymlinks {
				return 0, 0, "", &fs.PathError{Op: op, Path: original, Err: ErrLoop}
			}
			target, err := i.readLink(nid, basename)
			if err != nil {
				return 0, 0, "", err
			}
			// Prepend the symlink target to the remaining path
			if rest != "" {
				target = target + "/" + rest
			}
			// Resolve relative to the parent directory's full path
			if !path.IsAbs(target) {
				target = curPath + "/" + target
			}
			// Clean and re-resolve from root
			target = path.Clean(target)
			if len(target) > 0 && target[0] == '/' {
				target = target[1:]
			}
			nid = uint64(i.sb.RootNid)
			ftype = fs.ModeDir
			curPath = ""
			name = target
			if name == "." {
				name = ""
			}
			basename = name
			continue
		}

		if curPath == "" {
			curPath = basename
		} else {
			curPath = curPath + "/" + basename
		}
		name = rest
	}

	if basename == "" {
		basename = original
	}
	return nid, ftype, basename, nil
}

func (i *image) Open(name string) (fs.File, error) {
	nid, ftype, basename, err := i.resolve("open", name, true)
	if err != nil {
		return nil, err
	}
	b := file{img: i, name: basename, nid: nid, ftype: ftype}
	if ftype.IsDir() {
		return &dir{file: b}, nil
	}
	return &b, nil
}

func (i *image) Stat(name string) (fs.FileInfo, error) {
	nid, ftype, basename, err := i.resolve("stat", name, true)
	if err != nil {
		return nil, err
	}
	f := &file{img: i, name: basename, nid: nid, ftype: ftype}
	return f.readInfo(true)
}

// ReadFile reads the named file and returns its contents.
// Files larger than maxReadFileSize (128 MiB) are rejected;
// use Open and io.Copy for larger files.
func (i *image) ReadFile(name string) ([]byte, error) {
	nid, ftype, basename, err := i.resolve("readfile", name, true)
	if err != nil {
		return nil, err
	}
	if ftype.IsDir() {
		return nil, &fs.PathError{Op: "read", Path: name, Err: ErrIsDirectory}
	}
	f := &file{img: i, name: basename, nid: nid, ftype: ftype}
	fi, err := f.readInfo(false)
	if err != nil {
		return nil, err
	}
	if fi.size < 0 || fi.size > maxReadFileSize {
		return nil, fmt.Errorf("file size %d exceeds ReadFile limit %d; use Open and io.Copy for large files: %w", fi.size, int64(maxReadFileSize), ErrInvalid)
	}
	buf := make([]byte, fi.size)
	if fi.size > 0 {
		if _, err = f.Read(buf); err != nil && err != io.EOF {
			return nil, err
		}
	}
	return buf, nil
}

func (i *image) ReadDir(name string) ([]fs.DirEntry, error) {
	nid, ftype, basename, err := i.resolve("readdir", name, true)
	if err != nil {
		return nil, err
	}
	if !ftype.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ErrNotDirectory}
	}
	d := &dir{file: file{img: i, name: basename, nid: nid, ftype: ftype}}
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

func (i *image) ReadLink(name string) (string, error) {
	nid, ftype, basename, err := i.resolve("readlink", name, false)
	if err != nil {
		return "", err
	}
	if ftype&fs.ModeSymlink == 0 {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
	}
	return i.readLink(nid, basename)
}

func (i *image) Lstat(name string) (fs.FileInfo, error) {
	nid, ftype, basename, err := i.resolve("lstat", name, false)
	if err != nil {
		return nil, err
	}
	f := &file{img: i, name: basename, nid: nid, ftype: ftype}
	return f.readInfo(true)
}

type file struct {
	img   *image
	name  string
	nid   uint64
	ftype fs.FileMode

	// Mutable fields, open file should not be accessed concurrently
	offset int64     // current offset for read operations
	info   *fileInfo // cached fileInfo
}

func (b *file) readInfo(infoOnly bool) (fi *fileInfo, err error) {
	if b.info != nil {
		return b.info, nil
	}

	addr := b.img.metaStartPos() + int64(b.nid*disk.SizeInodeCompact)
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

	defer func() {
		v := recover()
		if v != nil {
			err = fmt.Errorf("file format error: %v", v)
		}
		if err != nil {
			b.img.putBlock(blk)
		}

	}()

	ino := blk.bytes()
	_, err = b.img.meta.ReadAt(ino, addr)
	if err != nil {
		return nil, err
	}

	var format, xcnt uint16
	if _, err = binary.Decode(ino[:2], binary.LittleEndian, &format); err != nil {
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
			nid:         b.nid,
			icsize:      disk.SizeInodeCompact,
			inodeLayout: layout,
			inodeData:   inode.InodeData,
			size:        int64(inode.Size),
			mode:        (fs.FileMode(inode.Mode) & ^fs.ModeType) | b.ftype,
			modTime:     time.Unix(int64(b.img.sb.BuildTime), int64(b.img.sb.BuildTimeNs)),
		}
		xcnt = inode.XattrCount
		if infoOnly {
			b.info.stat = &Stat{
				Mode:        disk.EroFSModeToGoFileMode(inode.Mode),
				Size:        int64(inode.Size),
				InodeLayout: layout,
				Inode:       int64(b.nid),
				Rdev:        disk.RdevFromMode(inode.Mode, inode.InodeData),
				UID:         uint32(inode.UID),
				GID:         uint32(inode.GID),
				Nlink:       int(inode.Nlink),
				Mtime:       b.img.sb.BuildTime,
				MtimeNs:     b.img.sb.BuildTimeNs,
			}
		}
		addr += disk.SizeInodeCompact
	} else {
		var inode disk.InodeExtended
		if _, err = binary.Decode(ino[:disk.SizeInodeExtended], binary.LittleEndian, &inode); err != nil {
			return nil, err
		}
		b.info = &fileInfo{
			name:        b.name,
			nid:         b.nid,
			icsize:      disk.SizeInodeExtended,
			inodeLayout: layout,
			inodeData:   inode.InodeData,
			size:        int64(inode.Size),
			mode:        (fs.FileMode(inode.Mode) & ^fs.ModeType) | b.ftype,
			modTime:     time.Unix(int64(inode.Mtime), int64(inode.MtimeNs)),
		}
		xcnt = inode.XattrCount
		if infoOnly {
			b.info.stat = &Stat{
				Mode:        disk.EroFSModeToGoFileMode(inode.Mode),
				Size:        int64(inode.Size),
				InodeLayout: layout,
				Inode:       int64(b.nid),
				Rdev:        disk.RdevFromMode(inode.Mode, inode.InodeData),
				UID:         inode.UID,
				GID:         inode.GID,
				Nlink:       int(inode.Nlink),
				Mtime:       inode.Mtime,
				MtimeNs:     inode.MtimeNs,
			}
		}
		addr += disk.SizeInodeExtended
	}

	if xcnt > 0 {
		b.info.xsize = int(xcnt-1)*disk.SizeXattrEntry + disk.SizeXattrBodyHeader
	}

	switch {
	case infoOnly && b.info.xsize > 0:
		if err = setXattrs(b, addr, blk); err != nil {
			return nil, err
		}
	case infoOnly || b.info.inodeLayout == disk.LayoutFlatPlain || b.info.size == 0 || blk.end != blkSize:
		b.img.putBlock(blk)
	default:
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
	if b.info != nil && b.info.cached != nil {
		b.img.putBlock(b.info.cached)
		b.info.cached = nil
	}
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

	// bn is the current block to read from (relative to file start)
	bn int

	// consumed is how many have been returned in the current block
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
				break
			}
			return nil, err
		}
		buf := b.bytes()
		if len(buf) < 12 {
			d.img.putBlock(b)
			break
		}

		var dirents [2]disk.Dirent

		readN, err := binary.Decode(buf[:12], binary.LittleEndian, &dirents[0])
		if err != nil {
			d.img.putBlock(b)
			return nil, fmt.Errorf("decode failed: %w", err)
		}
		if readN != 12 {
			d.img.putBlock(b)
			return nil, errors.New("invalid dirent: not fully decoded")
		}

		entryN := dirents[0].NameOff / disk.SizeDirent
		bufLen := len(buf)

		// Validate that NameOff is within bounds and dirent entries fit.
		if int(dirents[0].NameOff) > bufLen || entryN == 0 {
			d.img.putBlock(b)
			return ents, fmt.Errorf("invalid dirent name offset %d (buf size %d): %w", dirents[0].NameOff, bufLen, ErrInvalid)
		}

		for i := uint16(0); i < entryN; i++ {
			var name string
			if i < entryN-1 {
				start := int(disk.SizeDirent) * (int(i) + 1)
				if start+int(disk.SizeDirent) > bufLen {
					d.img.putBlock(b)
					return ents, fmt.Errorf("dirent entry %d exceeds block: %w", i+1, ErrInvalid)
				}
				readN, err := binary.Decode(buf[start:start+int(disk.SizeDirent)], binary.LittleEndian, &dirents[1])
				if err != nil {
					d.img.putBlock(b)
					return nil, fmt.Errorf("decode failed: %w", err)
				}
				if readN != 12 {
					d.img.putBlock(b)
					return nil, errors.New("invalid dirent: not fully decoded")
				}
				if int(dirents[0].NameOff) > bufLen || int(dirents[1].NameOff) > bufLen || dirents[1].NameOff < dirents[0].NameOff {
					d.img.putBlock(b)
					return ents, fmt.Errorf("invalid dirent name offset range [%d:%d] (buf size %d): %w",
						dirents[0].NameOff, dirents[1].NameOff, bufLen, ErrInvalid)
				}
				name = string(buf[dirents[0].NameOff:dirents[1].NameOff])
			} else {
				if int(dirents[0].NameOff) > bufLen {
					d.img.putBlock(b)
					return ents, fmt.Errorf("invalid dirent name offset %d (buf size %d): %w", dirents[0].NameOff, bufLen, ErrInvalid)
				}
				name = string(buf[dirents[0].NameOff:])
			}

			if i >= d.consumed && name != "." && name != ".." {
				f := file{
					img:   d.img,
					name:  name,
					nid:   dirents[0].Nid,
					ftype: disk.EroFSFtypeToFileMode(dirents[0].FileType),
				}
				ents = append(ents, &direntry{f})
				d.consumed = i + 1

				if n > 0 && len(ents) == n {
					if i == entryN-1 {
						d.consumed = 0
						d.bn++
					}
					d.img.putBlock(b)
					return ents, nil
				}
			}

			// Rotate next to current
			dirents[0] = dirents[1]
		}

		d.img.putBlock(b)
		d.consumed = 0
		d.bn++
		pos = int64(d.bn << d.img.sb.BlkSizeBits)
	}

	// Per fs.ReadDirFile contract: when n > 0 and we've reached the end
	// of the directory, return io.EOF. When n <= 0, return all entries
	// without io.EOF.
	if n > 0 {
		return ents, io.EOF
	}
	return ents, nil
}

// lookup searches for a directory entry by name using binary search.
// EROFS directories are sorted by name both within and across blocks.
// A cross-block binary search locates the correct block, then an
// intra-block binary search finds the entry.
// Returns the nid and file type if found, or fs.ErrNotExist if not.
func (d *dir) lookup(target string) (uint64, fs.FileMode, error) {
	fi, err := d.readInfo(false)
	if err != nil {
		return 0, 0, fmt.Errorf("readInfo failed: %w", err)
	}

	targetBytes := []byte(target)
	blkSize := int64(1 << d.img.sb.BlkSizeBits)
	nblocks := int((fi.size + blkSize - 1) / blkSize)

	// Binary search across blocks: compare target against the first
	// entry of each block to find which block may contain the target.
	// The last loaded block is retained to avoid reloading it for the
	// intra-block search.
	var lastBlk *block
	lastIdx := -1
	lo, hi := 0, nblocks
	for lo < hi {
		mid := lo + (hi-lo)/2
		pos := int64(mid) * blkSize
		b, err := d.img.loadBlock(fi, pos)
		if err != nil {
			if errors.Is(err, io.EOF) {
				hi = mid
				continue
			}
			if lastBlk != nil {
				d.img.putBlock(lastBlk)
			}
			return 0, 0, err
		}
		buf := b.bytes()
		firstName, err := blockFirstName(buf)
		if err != nil {
			d.img.putBlock(b)
			if lastBlk != nil {
				d.img.putBlock(lastBlk)
			}
			return 0, 0, err
		}

		if bytes.Compare(firstName, targetBytes) <= 0 {
			// This block's first entry <= target; keep it as candidate.
			if lastBlk != nil {
				d.img.putBlock(lastBlk)
			}
			lastBlk = b
			lastIdx = mid
			lo = mid + 1
		} else {
			d.img.putBlock(b)
			hi = mid
		}
	}

	// lastIdx is the last block whose first entry <= target.
	// The target must be in that block if it exists.
	if lastIdx < 0 {
		return 0, 0, fs.ErrNotExist
	}

	buf := lastBlk.bytes()
	nid, ftype, err := lookupBlock(buf, targetBytes)
	d.img.putBlock(lastBlk)
	return nid, ftype, err
}

// blockFirstName returns the name of the first entry in a directory block.
func blockFirstName(buf []byte) ([]byte, error) {
	if len(buf) < disk.SizeDirent {
		return nil, fmt.Errorf("directory block too small: %w", ErrInvalid)
	}
	var first disk.Dirent
	if _, err := binary.Decode(buf[:disk.SizeDirent], binary.LittleEndian, &first); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	entryN := first.NameOff / disk.SizeDirent
	if entryN == 0 || int(first.NameOff) > len(buf) {
		return nil, fmt.Errorf("invalid name offset %d: %w", first.NameOff, ErrInvalid)
	}
	var nameEnd uint16
	if entryN > 1 {
		nextOff := int(disk.SizeDirent) + 8
		if nextOff+2 > len(buf) {
			return nil, fmt.Errorf("next dirent name offset out of range: %w", ErrInvalid)
		}
		nameEnd = binary.LittleEndian.Uint16(buf[nextOff:])
	} else {
		nameEnd = uint16(len(buf))
	}
	if first.NameOff > nameEnd || int(nameEnd) > len(buf) {
		return nil, fmt.Errorf("name range [%d:%d] out of bounds: %w", first.NameOff, nameEnd, ErrInvalid)
	}
	name := buf[first.NameOff:nameEnd]
	// Trim NUL terminator if present
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return name, nil
}

// blockDirent decodes the dirent at index i from buf and returns the
// name bytes for that entry. entryN is the total number of entries.
func blockDirent(buf []byte, i, entryN uint16) (disk.Dirent, []byte, error) {
	var de disk.Dirent
	off := int(disk.SizeDirent * i)
	if off+disk.SizeDirent > len(buf) {
		return de, nil, fmt.Errorf("dirent %d offset %d out of range: %w", i, off, ErrInvalid)
	}
	if _, err := binary.Decode(buf[off:off+disk.SizeDirent], binary.LittleEndian, &de); err != nil {
		return de, nil, fmt.Errorf("decode dirent %d failed: %w", i, err)
	}
	var nameEnd uint16
	if i < entryN-1 {
		nextOff := int(disk.SizeDirent*(i+1)) + 8
		if nextOff+2 > len(buf) {
			return de, nil, fmt.Errorf("dirent %d next name offset out of range: %w", i, ErrInvalid)
		}
		nameEnd = binary.LittleEndian.Uint16(buf[nextOff:])
	} else {
		nameEnd = uint16(len(buf))
	}
	if de.NameOff > nameEnd || int(nameEnd) > len(buf) {
		return de, nil, fmt.Errorf("dirent %d name range [%d:%d] out of bounds: %w", i, de.NameOff, nameEnd, ErrInvalid)
	}
	name := buf[de.NameOff:nameEnd]
	// The last entry name may be NUL-terminated before the end of the block.
	if i == entryN-1 {
		if j := bytes.IndexByte(name, 0); j >= 0 {
			name = name[:j]
		}
	}
	return de, name, nil
}

// lookupBlock searches a single directory block for the target name
// using binary search.
func lookupBlock(buf, target []byte) (uint64, fs.FileMode, error) {
	if len(buf) < disk.SizeDirent {
		return 0, 0, fmt.Errorf("directory block too small: %w", ErrInvalid)
	}
	var first disk.Dirent
	if _, err := binary.Decode(buf[:disk.SizeDirent], binary.LittleEndian, &first); err != nil {
		return 0, 0, fmt.Errorf("decode failed: %w", err)
	}
	if first.NameOff%disk.SizeDirent != 0 {
		return 0, 0, fmt.Errorf("invalid name offset %d not aligned to dirent size: %w", first.NameOff, ErrInvalid)
	}
	entryN := first.NameOff / disk.SizeDirent
	if int(first.NameOff) > len(buf) {
		return 0, 0, fmt.Errorf("name offset %d exceeds block size %d: %w", first.NameOff, len(buf), ErrInvalid)
	}

	lo, hi := uint16(0), entryN
	for lo < hi {
		mid := lo + (hi-lo)/2
		de, name, err := blockDirent(buf, mid, entryN)
		if err != nil {
			return 0, 0, err
		}
		switch bytes.Compare(name, target) {
		case 0:
			return de.Nid, disk.EroFSFtypeToFileMode(de.FileType), nil
		case -1:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return 0, 0, fs.ErrNotExist
}

type fileInfo struct {
	name        string
	nid         uint64
	icsize      int8
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

func (fi *fileInfo) flatDataOffset() int64 {
	// inode core size + xattr size
	return int64(fi.icsize) + int64(fi.xsize)
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

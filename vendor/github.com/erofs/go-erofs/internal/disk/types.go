package disk

const (
	MagicNumber      = 0xe0f5e1e2
	SuperBlockOffset = 1024

	FeatureIncompatLZ4_0Padding         = 0x1
	FeatureIncompatChunkedFile          = 0x4
	FeatureIncompatDeviceTable          = 0x8
	FeatureIncompatFragments            = 0x20
	FeatureIncompatXattrPrefixes        = 0x40
	FeatureIncompatAll           uint32 = FeatureIncompatLZ4_0Padding |
		FeatureIncompatChunkedFile | FeatureIncompatDeviceTable |
		FeatureIncompatFragments | FeatureIncompatXattrPrefixes

	SizeSuperBlock      = 128
	SizeInodeCompact    = 32
	SizeInodeExtended   = 64
	SizeDirent          = 12
	SizeXattrBodyHeader = 12
	SizeXattrEntry      = 4

	LayoutFlatPlain         = 0
	LayoutCompressedFull    = 1
	LayoutFlatInline        = 2
	LayoutCompressedCompact = 3
	LayoutChunkBased        = 4

	LayoutChunkFormatBits    = 0x001F
	LayoutChunkFormatIndexes = 0x0020
	LayoutChunkFormat48Bit   = 0x0040
)

// SuperBlock represents the EROFS on-disk superblock.
// See: https://docs.kernel.org/filesystems/erofs.html#on-disk-layout
type SuperBlock struct {
	MagicNumber      uint32
	Checksum         uint32
	FeatureCompat    uint32
	BlkSizeBits      uint8
	ExtSlots         uint8
	RootNid          uint16
	Inos             uint64
	BuildTime        uint64
	BuildTimeNs      uint32
	Blocks           uint32
	MetaBlkAddr      uint32
	XattrBlkAddr     uint32
	UUID             [16]uint8
	VolumeName       [16]uint8
	FeatureIncompat  uint32
	ComprAlgs        uint16
	ExtraDevices     uint16
	DevtSlotOff      uint16
	DirBlkBits       uint8
	XattrPrefixCount uint8
	XattrPrefixStart uint32
	PackedNid        uint64 // Nid of the special "packed" inode for shared data/prefixes
	XattrFilterRes   uint8
	Reserved         [23]uint8
}

// InodeCompact represents the 32-byte on-disk compact inode.
type InodeCompact struct {
	Format     uint16 // i_format
	XattrCount uint16 // i_xattr_icount
	Mode       uint16 // i_mode
	Nlink      uint16 // i_nlink
	Size       uint32 // i_size
	Reserved   uint32 // i_reserved
	InodeData  uint32 // i_u (i_raw_blkaddr, i_rdev, etc.)
	Inode      uint32 // i_ino
	UID        uint16 // i_uid
	GID        uint16 // i_gid
	Reserved2  uint32 // i_reserved2
}

// InodeExtended represents the 64-byte on-disk extended inode.
type InodeExtended struct {
	Format     uint16 // i_format
	XattrCount uint16 // i_xattr_icount
	Mode       uint16 // i_mode
	Reserved   uint16 // i_reserved
	Size       uint64 // i_size
	InodeData  uint32 // i_u (i_raw_blkaddr, i_rdev, etc.)
	Inode      uint32 // i_ino
	UID        uint32 // i_uid
	GID        uint32 // i_gid
	Mtime      uint64 // i_mtime
	MtimeNs    uint32 // i_mtime_nsec
	Nlink      uint32 // i_nlink
	Reserved2  [16]uint8
}

type Dirent struct {
	Nid      uint64
	NameOff  uint16
	FileType uint8
	Reserved uint8
}

// XattrHeader is the header after an inode containing xattr information
//
// Original definition:
// inline xattrs (n == i_xattr_icount):
// erofs_xattr_ibody_header(1) + (n - 1) * 4 bytes
//
//	12 bytes           /                   \
//	                  /                     \
//	                 /-----------------------\
//	                 |  erofs_xattr_entries+ |
//	                 +-----------------------+
//
// inline xattrs must starts in erofs_xattr_ibody_header,
// for read-only fs, no need to introduce h_refcount
// Actual name is prefix | long prefix (prefix + infix) + name
type XattrHeader struct {
	NameFilter  uint32 // bit value 1 indicate not-present
	SharedCount uint8
	Reserved    [7]uint8
}

type XattrEntry struct {
	NameLen   uint8  // length of name
	NameIndex uint8  // index of name in XattrHeader, 0x80 set indicates long prefix at index&0x7F + XattrPrefixStart
	ValueLen  uint16 // length of value
	// Name+Value
}

type XattrLongPrefixitem struct {
	PrefixAddr uint32 // address of the long prefix
	PrefixLen  uint8  // length of the long prefix
}

type XattrLongPrefix struct {
	BaseIndex uint8 // short xattr name prefix index
	// Infix part after short prefix
}

type InodeChunkIndex struct {
	StartBlkHi uint16 // part of 48-bit support (not yet implemented)
	DeviceID   uint16
	StartBlkLo uint32
}

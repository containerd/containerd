package disk

const (
	MagicNumber      = 0xe0f5e1e2
	SuperBlockOffset = 1024

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
	PackedNid        uint64
	XattrFilterRes   uint8
	Reserved         [23]uint8
}

type InodeCompact struct {
	Format       uint16
	XattrCount   uint16
	Mode         uint16
	Nlink        uint16
	Size         uint32
	Reserved     uint32
	InodeData    uint32
	Inode        uint32
	UID          uint16
	GID          uint16
	Reserved2    uint32
}

type InodeExtended struct {
	Format     uint16
	XattrCount uint16
	Mode       uint16
	Reserved   uint16
	Size       uint64
	InodeData  uint32 // RawBlockAddr | Rdev | Compressed Count | Chunk Format
	Inode      uint32
	UID        uint32
	GID        uint32
	Mtime      uint64
	MtimeNs    uint32
	Nlink      uint32
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
// Original defintion:
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
	StartBlkHi uint16
	DeviceId   uint16
	StartBlkLo uint32
}

package btrfs

import (
	"fmt"
	"time"
	"unsafe"
)

const (
	_BTRFS_BLOCK_GROUP_TYPE_MASK = (blockGroupData |
		blockGroupSystem |
		blockGroupMetadata)
	_BTRFS_BLOCK_GROUP_PROFILE_MASK = (blockGroupRaid0 |
		blockGroupRaid1 |
		blockGroupRaid5 |
		blockGroupRaid6 |
		blockGroupDup |
		blockGroupRaid10)
	_BTRFS_BLOCK_GROUP_MASK = _BTRFS_BLOCK_GROUP_TYPE_MASK | _BTRFS_BLOCK_GROUP_PROFILE_MASK
)

type rootRef struct {
	DirID    objectID
	Sequence uint64
	Name     string
}

func (rootRef) btrfsSize() int { return 18 }

func asUint64(p []byte) uint64 {
	return *(*uint64)(unsafe.Pointer(&p[0]))
}

func asUint32(p []byte) uint32 {
	return *(*uint32)(unsafe.Pointer(&p[0]))
}

func asUint16(p []byte) uint16 {
	return *(*uint16)(unsafe.Pointer(&p[0]))
}

func asRootRef(p []byte) rootRef {
	const sz = 18
	// assuming that it is highly unsafe to have sizeof(struct) > len(data)
	// (*btrfs_root_ref)(unsafe.Pointer(&p[0])) and sizeof(btrfs_root_ref) == 24
	ref := rootRef{
		DirID:    objectID(asUint64(p[0:])),
		Sequence: asUint64(p[8:]),
	}
	if n := asUint16(p[16:]); n > 0 {
		ref.Name = string(p[sz : sz+n : sz+n])
	}
	return ref
}

var treeKeyNames = map[treeKeyType]string{
	inodeItemKey:          "inodeItem",
	inodeRefKey:           "inodeRef",
	inodeExtrefKey:        "inodeExtref",
	xattrItemKey:          "xattrItemKey",
	orphanItemKey:         "orphanItem",
	dirLogItemKey:         "dirLogItem",
	dirLogIndexKey:        "dirLogIndex",
	dirItemKey:            "dirItem",
	dirIndexKey:           "dirIndex",
	extentDataKey:         "extentData",
	extentCsumKey:         "extentCsum",
	rootItemKey:           "rootItem",
	rootBackrefKey:        "rootBackref",
	rootRefKey:            "rootRef",
	extentItemKey:         "extentItem",
	metadataItemKey:       "metadataItem",
	treeBlockRefKey:       "treeBlockRef",
	extentDataRefKey:      "extentDataRef",
	extentRefV0Key:        "extentRefV0",
	sharedBlockRefKey:     "sharedBlockRef",
	sharedDataRefKey:      "sharedDataRef",
	blockGroupItemKey:     "blockGroupItem",
	freeSpaceInfoKey:      "freeSpaceInfo",
	freeSpaceExtentKey:    "freeSpaceExtent",
	freeSpaceBitmapKey:    "freeSpaceBitmap",
	devExtentKey:          "devExtent",
	devItemKey:            "devItem",
	chunkItemKey:          "chunkItem",
	qgroupStatusKey:       "qgroupStatus",
	qgroupInfoKey:         "qgroupInfo",
	qgroupLimitKey:        "qgroupLimit",
	qgroupRelationKey:     "qgroupRelation",
	temporaryItemKey:      "temporaryItem",
	persistentItemKey:     "persistentItem",
	devReplaceKey:         "devReplace",
	uuidKeySubvol:         "uuidKeySubvol",
	uuidKeyReceivedSubvol: "uuidKeyReceivedSubvol",
	stringItemKey:         "stringItem",
}

func (t treeKeyType) String() string {
	if name, ok := treeKeyNames[t]; ok {
		return name
	}
	return fmt.Sprintf("%#x", int(t))
}

// btrfs_disk_key_raw is a raw bytes for btrfs_disk_key structure
type btrfs_disk_key_raw [17]byte

func (p btrfs_disk_key_raw) Decode() diskKey {
	return diskKey{
		ObjectID: asUint64(p[0:]),
		Type:     p[8],
		Offset:   asUint64(p[9:]),
	}
}

type diskKey struct {
	ObjectID uint64
	Type     byte
	Offset   uint64
}

// btrfs_timespec_raw is a raw bytes for btrfs_timespec structure.
type btrfs_timespec_raw [12]byte

func (t btrfs_timespec_raw) Decode() time.Time {
	sec, nsec := asUint64(t[0:]), asUint32(t[8:])
	return time.Unix(int64(sec), int64(nsec))
}

// timeBlock is a raw set of bytes for 4 time fields.
// It is used to keep correct alignment when accessing structures from btrfs.
type timeBlock [4]btrfs_timespec_raw

type btrfs_inode_item_raw struct {
	generation  uint64
	transid     uint64
	size        uint64
	nbytes      uint64
	block_group uint64
	nlink       uint32
	uid         uint32
	gid         uint32
	mode        uint32
	rdev        uint64
	flags       uint64
	sequence    uint64
	_           [4]uint64 // reserved
	// 	atime btrfs_timespec
	// 	ctime btrfs_timespec
	// 	mtime btrfs_timespec
	// 	otime btrfs_timespec
	times timeBlock
}

func (v btrfs_inode_item_raw) Decode() inodeItem {
	return inodeItem{
		Gen:        v.generation,
		TransID:    v.transid,
		Size:       v.size,
		NBytes:     v.nbytes,
		BlockGroup: v.block_group,
		NLink:      v.nlink,
		UID:        v.uid,
		GID:        v.gid,
		Mode:       v.mode,
		RDev:       v.rdev,
		Flags:      v.flags,
		Sequence:   v.sequence,
		ATime:      v.times[0].Decode(),
		CTime:      v.times[1].Decode(),
		MTime:      v.times[2].Decode(),
		OTime:      v.times[3].Decode(),
	}
}

type inodeItem struct {
	Gen        uint64 // nfs style generation number
	TransID    uint64 // transid that last touched this inode
	Size       uint64
	NBytes     uint64
	BlockGroup uint64
	NLink      uint32
	UID        uint32
	GID        uint32
	Mode       uint32
	RDev       uint64
	Flags      uint64
	Sequence   uint64 // modification sequence number for NFS
	ATime      time.Time
	CTime      time.Time
	MTime      time.Time
	OTime      time.Time
}

func asRootItem(p []byte) *btrfs_root_item_raw {
	return (*btrfs_root_item_raw)(unsafe.Pointer(&p[0]))
}

type btrfs_root_item_raw [439]byte

func (p btrfs_root_item_raw) Decode() rootItem {
	const (
		off2 = unsafe.Sizeof(btrfs_root_item_raw_p1{})
		off3 = off2 + 23
	)
	p1 := (*btrfs_root_item_raw_p1)(unsafe.Pointer(&p[0]))
	p2 := p[off2 : off2+23]
	p2_k := (*btrfs_disk_key_raw)(unsafe.Pointer(&p[off2+4]))
	p2_b := p2[4+17:]
	p3 := (*btrfs_root_item_raw_p3)(unsafe.Pointer(&p[off3]))
	return rootItem{
		Inode:        p1.inode.Decode(),
		Gen:          p1.generation,
		RootDirID:    p1.root_dirid,
		ByteNr:       p1.bytenr,
		ByteLimit:    p1.byte_limit,
		BytesUsed:    p1.bytes_used,
		LastSnapshot: p1.last_snapshot,
		Flags:        p1.flags,
		// from here, Go structure become misaligned with C structure
		Refs:         asUint32(p2[0:]),
		DropProgress: p2_k.Decode(),
		DropLevel:    p2_b[0],
		Level:        p2_b[1],
		// these fields are still misaligned by 1 bytes
		// TODO(dennwc): it's a copy of Gen to check structure version; hide it maybe?
		GenV2:        p3.generation_v2,
		UUID:         p3.uuid,
		ParentUUID:   p3.parent_uuid,
		ReceivedUUID: p3.received_uuid,
		CTransID:     p3.ctransid,
		OTransID:     p3.otransid,
		STransID:     p3.stransid,
		RTransID:     p3.rtransid,
		CTime:        p3.times[0].Decode(),
		OTime:        p3.times[1].Decode(),
		STime:        p3.times[2].Decode(),
		RTime:        p3.times[3].Decode(),
	}
}

type rootItem struct {
	Inode        inodeItem
	Gen          uint64
	RootDirID    uint64
	ByteNr       uint64
	ByteLimit    uint64
	BytesUsed    uint64
	LastSnapshot uint64
	Flags        uint64
	Refs         uint32
	DropProgress diskKey
	DropLevel    uint8
	Level        uint8
	GenV2        uint64
	UUID         UUID
	ParentUUID   UUID
	ReceivedUUID UUID
	CTransID     uint64
	OTransID     uint64
	STransID     uint64
	RTransID     uint64
	CTime        time.Time
	OTime        time.Time
	STime        time.Time
	RTime        time.Time
}

type btrfs_root_item_raw_p1 struct {
	inode         btrfs_inode_item_raw
	generation    uint64
	root_dirid    uint64
	bytenr        uint64
	byte_limit    uint64
	bytes_used    uint64
	last_snapshot uint64
	flags         uint64
}
type btrfs_root_item_raw_p2 struct {
	refs          uint32
	drop_progress btrfs_disk_key_raw
	drop_level    uint8
	level         uint8
}
type btrfs_root_item_raw_p3 struct {
	generation_v2 uint64
	uuid          UUID
	parent_uuid   UUID
	received_uuid UUID
	ctransid      uint64
	otransid      uint64
	stransid      uint64
	rtransid      uint64
	// ctime btrfs_timespec
	// otime btrfs_timespec
	// stime btrfs_timespec
	// rtime btrfs_timespec
	times timeBlock
	_     [8]uint64 // reserved
}

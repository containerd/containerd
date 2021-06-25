package btrfs

// This code was auto-generated; DO NOT EDIT!

type treeKeyType uint32

type objectID uint64

type fileType int

type fileExtentType int

type devReplaceItemState int

type blockGroup uint64

// This header contains the structure definitions and constants used
// by file system objects that can be retrieved using
// the BTRFS_IOC_SEARCH_TREE ioctl. That means basically anything that
// is needed to describe a leaf node's key or item contents.

const (
	// Holds pointers to all of the tree roots
	rootTreeObjectid objectID = 1

	// Stores information about which extents are in use, and reference counts
	extentTreeObjectid objectID = 2

	// Chunk tree stores translations from logical -> physical block numbering
	// the super block points to the chunk tree
	chunkTreeObjectid objectID = 3

	// Stores information about which areas of a given device are in use.
	// one per device. The tree of tree roots points to the device tree
	devTreeObjectid objectID = 4

	// One per subvolume, storing files and directories
	fsTreeObjectid objectID = 5

	// Directory objectid inside the root tree
	rootTreeDirObjectid objectID = 6

	// Holds checksums of all the data extents
	csumTreeObjectid objectID = 7

	// Holds quota configuration and tracking
	quotaTreeObjectid objectID = 8

	// For storing items that use the BTRFS_UUID_KEY* types
	uuidTreeObjectid objectID = 9

	// Tracks free space in block groups.
	freeSpaceTreeObjectid objectID = 10

	// Device stats in the device tree
	devStatsObjectid objectID = 0

	// For storing balance parameters in the root tree
	balanceObjectid objectID = (1<<64 - 4)

	// Orhpan objectid for tracking unlinked/truncated files
	orphanObjectid objectID = (1<<64 - 5)

	// Does write ahead logging to speed up fsyncs
	treeLogObjectid      objectID = (1<<64 - 6)
	treeLogFixupObjectid objectID = (1<<64 - 7)

	// For space balancing
	treeRelocObjectid     objectID = (1<<64 - 8)
	dataRelocTreeObjectid objectID = (1<<64 - 9)

	// Extent checksums all have this objectid
	// this allows them to share the logging tree
	// for fsyncs
	extentCsumObjectid objectID = (1<<64 - 10)

	// For storing free space cache
	freeSpaceObjectid objectID = (1<<64 - 11)

	// The inode number assigned to the special inode for storing
	// free ino cache
	freeInoObjectid objectID = (1<<64 - 12)

	// Dummy objectid represents multiple objectids
	multipleObjectids = (1<<64 - 255)

	// All files have objectids in this range.
	firstFreeObjectid      objectID = 256
	lastFreeObjectid       objectID = (1<<64 - 256)
	firstChunkTreeObjectid objectID = 256

	// The device items go into the chunk tree. The key is in the form
	// [ 1 BTRFS_DEV_ITEM_KEY device_id ]
	devItemsObjectid objectID = 1

	btreeInodeObjectid objectID = 1

	emptySubvolDirObjectid objectID = 2

	devReplaceDevid = 0

	// Inode items have the data typically returned from stat and store other
	// info about object characteristics. There is one for every file and dir in
	// the FS
	inodeItemKey   treeKeyType = 1
	inodeRefKey    treeKeyType = 12
	inodeExtrefKey treeKeyType = 13
	xattrItemKey   treeKeyType = 24
	orphanItemKey  treeKeyType = 48
	// Reserve 2-15 close to the inode for later flexibility

	// Dir items are the name -> inode pointers in a directory. There is one
	// for every name in a directory.
	dirLogItemKey  treeKeyType = 60
	dirLogIndexKey treeKeyType = 72
	dirItemKey     treeKeyType = 84
	dirIndexKey    treeKeyType = 96
	// Extent data is for file data
	extentDataKey treeKeyType = 108

	// Extent csums are stored in a separate tree and hold csums for
	// an entire extent on disk.
	extentCsumKey treeKeyType = 128

	// Root items point to tree roots. They are typically in the root
	// tree used by the super block to find all the other trees
	rootItemKey treeKeyType = 132

	// Root backrefs tie subvols and snapshots to the directory entries that
	// reference them
	rootBackrefKey treeKeyType = 144

	// Root refs make a fast index for listing all of the snapshots and
	// subvolumes referenced by a given root. They point directly to the
	// directory item in the root that references the subvol
	rootRefKey treeKeyType = 156

	// Extent items are in the extent map tree. These record which blocks
	// are used, and how many references there are to each block
	extentItemKey treeKeyType = 168

	// The same as the BTRFS_EXTENT_ITEM_KEY, except it's metadata we already know
	// the length, so we save the level in key->offset instead of the length.
	metadataItemKey treeKeyType = 169

	treeBlockRefKey treeKeyType = 176

	extentDataRefKey treeKeyType = 178

	extentRefV0Key treeKeyType = 180

	sharedBlockRefKey treeKeyType = 182

	sharedDataRefKey treeKeyType = 184

	// Block groups give us hints into the extent allocation trees. Which
	// blocks are free etc etc
	blockGroupItemKey treeKeyType = 192

	// Every block group is represented in the free space tree by a free space info
	// item, which stores some accounting information. It is keyed on
	// (block_group_start, FREE_SPACE_INFO, block_group_length).
	freeSpaceInfoKey treeKeyType = 198

	// A free space extent tracks an extent of space that is free in a block group.
	// It is keyed on (start, FREE_SPACE_EXTENT, length).
	freeSpaceExtentKey treeKeyType = 199

	// When a block group becomes very fragmented, we convert it to use bitmaps
	// instead of extents. A free space bitmap is keyed on
	// (start, FREE_SPACE_BITMAP, length); the corresponding item is a bitmap with
	// (length / sectorsize) bits.
	freeSpaceBitmapKey treeKeyType = 200

	devExtentKey treeKeyType = 204
	devItemKey   treeKeyType = 216
	chunkItemKey treeKeyType = 228

	// Records the overall state of the qgroups.
	// There's only one instance of this key present,
	// (0, BTRFS_QGROUP_STATUS_KEY, 0)
	qgroupStatusKey treeKeyType = 240
	// Records the currently used space of the qgroup.
	// One key per qgroup, (0, BTRFS_QGROUP_INFO_KEY, qgroupid).
	qgroupInfoKey treeKeyType = 242
	// Contains the user configured limits for the qgroup.
	// One key per qgroup, (0, BTRFS_QGROUP_LIMIT_KEY, qgroupid).
	qgroupLimitKey treeKeyType = 244
	// Records the child-parent relationship of qgroups. For
	// each relation, 2 keys are present:
	// (childid, BTRFS_QGROUP_RELATION_KEY, parentid)
	// (parentid, BTRFS_QGROUP_RELATION_KEY, childid)
	qgroupRelationKey treeKeyType = 246

	// Obsolete name, see BTRFS_TEMPORARY_ITEM_KEY.
	balanceItemKey treeKeyType = 248

	// The key type for tree items that are stored persistently, but do not need to
	// exist for extended period of time. The items can exist in any tree.
	// [subtype, BTRFS_TEMPORARY_ITEM_KEY, data]
	// Existing items:
	// - balance status item
	// (BTRFS_BALANCE_OBJECTID, BTRFS_TEMPORARY_ITEM_KEY, 0)
	temporaryItemKey treeKeyType = 248

	// Obsolete name, see BTRFS_PERSISTENT_ITEM_KEY
	devStatsKey treeKeyType = 249

	// The key type for tree items that are stored persistently and usually exist
	// for a long period, eg. filesystem lifetime. The item kinds can be status
	// information, stats or preference values. The item can exist in any tree.
	// [subtype, BTRFS_PERSISTENT_ITEM_KEY, data]
	// Existing items:
	// - device statistics, store IO stats in the device tree, one key for all
	// stats
	// (BTRFS_DEV_STATS_OBJECTID, BTRFS_DEV_STATS_KEY, 0)
	persistentItemKey treeKeyType = 249

	// Persistantly stores the device replace state in the device tree.
	// The key is built like this: (0, BTRFS_DEV_REPLACE_KEY, 0).
	devReplaceKey treeKeyType = 250

	// Stores items that allow to quickly map UUIDs to something else.
	// These items are part of the filesystem UUID tree.
	// The key is built like this:
	// (UUID_upper_64_bits, BTRFS_UUID_KEY*, UUID_lower_64_bits).
	uuidKeySubvol         = 251
	uuidKeyReceivedSubvol = 252

	// String items are for debugging. They just store a short string of
	// data in the FS
	stringItemKey treeKeyType = 253

	// 32 bytes in various csum fields
	csumSize = 32

	// Csum types
	csumTypeCrc32 = 0

	// Flags definitions for directory entry item type
	// Used by:
	// struct btrfs_dir_item.type
	ftUnknown fileType = 0
	ftRegFile fileType = 1
	ftDir     fileType = 2
	ftChrdev  fileType = 3
	ftBlkdev  fileType = 4
	ftFifo    fileType = 5
	ftSock    fileType = 6
	ftSymlink fileType = 7
	ftXattr   fileType = 8
	ftMax     fileType = 9

	// The key defines the order in the tree, and so it also defines (optimal)
	// block layout.
	// objectid corresponds to the inode number.
	// type tells us things about the object, and is a kind of stream selector.
	// so for a given inode, keys with type of 1 might refer to the inode data,
	// type of 2 may point to file data in the btree and type == 3 may point to
	// extents.
	// offset is the starting byte offset for this key in the stream.
	// btrfs_disk_key is in disk byte order. struct btrfs_key is always
	// in cpu native order. Otherwise they are identical and their sizes
	// should be the same (ie both packed)

	// The internal btrfs device id

	// Size of the device

	// Bytes used

	// Optimal io alignment for this device

	// Optimal io width for this device

	// Minimal io size for this device

	// Type and info about this device

	// Expected generation for this device

	// Starting byte of this partition on the device,
	// to allow for stripe alignment in the future

	// Grouping information for allocation decisions

	// Seek speed 0-100 where 100 is fastest

	// Bandwidth 0-100 where 100 is fastest

	// Btrfs generated uuid for this device

	// Uuid of FS who owns this device

	// Size of this chunk in bytes

	// Objectid of the root referencing this chunk

	// Optimal io alignment for this chunk

	// Optimal io width for this chunk

	// Minimal io size for this chunk

	// 2^16 stripes is quite a lot, a second limit is the size of a single
	// item in the btree

	// Sub stripes only matter for raid10
	// Additional stripes go here

	freeSpaceExtent = 1
	freeSpaceBitmap = 2

	headerFlagWritten = (1 << 0)
	headerFlagReloc   = (1 << 1)

	// Super block flags
	// Errors detected
	superFlagError = (1 << 2)

	superFlagSeeding  = (1 << 32)
	superFlagMetadump = (1 << 33)

	// Items in the extent btree are used to record the objectid of the
	// owner of the block and the number of references

	extentFlagData      = (1 << 0)
	extentFlagTreeBlock = (1 << 1)

	// Following flags only apply to tree blocks

	// Use full backrefs for extent pointers in the block
	blockFlagFullBackref = (1 << 8)

	// This flag is only used internally by scrub and may be changed at any time
	// it is only declared here to avoid collisions
	extentFlagSuper = (1 << 48)

	// Old style backrefs item

	// Dev extents record free space on individual devices. The owner
	// field points back to the chunk allocation mapping tree that allocated
	// the extent. The chunk tree uuid field is a way to double check the owner

	// Name goes here

	// Name goes here

	// Nfs style generation number
	// Transid that last touched this inode

	// Modification sequence number for NFS

	// A little future expansion, for more than this we can
	// just grow the inode item and version it

	rootSubvolRdonly = (1 << 0)

	// Internal in-memory flag that a subvolume has been marked for deletion but
	// still visible as a directory
	rootSubvolDead = (1 << 48)

	// The following fields appear after subvol_uuids+subvol_times
	// were introduced.

	// This generation number is used to test if the new fields are valid
	// and up to date while reading the root item. Every time the root item
	// is written out, the "generation" field is copied into this field. If
	// anyone ever mounted the fs with an older kernel, we will have
	// mismatching generation values here and thus must invalidate the
	// new fields. See btrfs_update_root and btrfs_find_last_root for
	// details.
	// the offset of generation_v2 is also used as the start for the memset
	// when invalidating the fields.

	// This is used for both forward and backward root refs

	// Profiles to operate on, single is denoted by
	// BTRFS_AVAIL_ALLOC_BIT_SINGLE

	// Usage filter
	// BTRFS_BALANCE_ARGS_USAGE with a single value means '0..N'
	// BTRFS_BALANCE_ARGS_USAGE_RANGE - range syntax, min..max

	// Devid filter

	// Devid subset filter [pstart..pend)

	// Btrfs virtual address space subset filter [vstart..vend)

	// Profile to convert to, single is denoted by
	// BTRFS_AVAIL_ALLOC_BIT_SINGLE

	// BTRFS_BALANCE_ARGS_*

	// BTRFS_BALANCE_ARGS_LIMIT with value 'limit'
	// BTRFS_BALANCE_ARGS_LIMIT_RANGE - the extend version can use minimum
	// and maximum

	// Process chunks that cross stripes_min..stripes_max devices,
	// BTRFS_BALANCE_ARGS_STRIPES_RANGE

	// Store balance parameters to disk so that balance can be properly
	// resumed after crash or unmount
	// BTRFS_BALANCE_*

	fileExtentInline   fileExtentType = 0
	fileExtentReg      fileExtentType = 1
	fileExtentPrealloc fileExtentType = 2

	// Transaction id that created this extent
	// Max number of bytes to hold this extent in ram
	// when we split a compressed extent we can't know how big
	// each of the resulting pieces will be. So, this is
	// an upper limit on the size of the extent in ram instead of
	// an exact limit.

	// 32 bits for the various ways we might encode the data,
	// including compression and encryption. If any of these
	// are set to something a given disk format doesn't understand
	// it is treated like an incompat flag for reading and writing,
	// but not for stat.

	// Are we inline data or a real extent?

	// Disk space consumed by the extent, checksum blocks are included
	// in these numbers
	// At this offset in the structure, the inline extent data start.
	// The logical offset in file blocks (no csums)
	// this extent record is for. This allows a file extent to point
	// into the middle of an existing extent on disk, sharing it
	// between two snapshots (useful if some bytes in the middle of the
	// extent have changed
	// The logical number of file blocks (no csums included). This
	// always reflects the size uncompressed and without encoding.

	// Grow this item struct at the end for future enhancements and keep
	// the existing values unchanged

	devReplaceItemContReadingFromSrcdevModeAlways                     = 0
	devReplaceItemContReadingFromSrcdevModeAvoid                      = 1
	devReplaceItemStateNeverStarted               devReplaceItemState = 0
	devReplaceItemStateStarted                    devReplaceItemState = 1
	devReplaceItemStateSuspended                  devReplaceItemState = 2
	devReplaceItemStateFinished                   devReplaceItemState = 3
	devReplaceItemStateCanceled                   devReplaceItemState = 4

	// Grow this item struct at the end for future enhancements and keep
	// the existing values unchanged

	// Different types of block groups (and chunks)
	blockGroupData     blockGroup = (1 << 0)
	blockGroupSystem   blockGroup = (1 << 1)
	blockGroupMetadata blockGroup = (1 << 2)
	blockGroupRaid0    blockGroup = (1 << 3)
	blockGroupRaid1    blockGroup = (1 << 4)
	blockGroupDup      blockGroup = (1 << 5)
	blockGroupRaid10   blockGroup = (1 << 6)
	blockGroupRaid5    blockGroup = (1 << 7)
	blockGroupRaid6    blockGroup = (1 << 8)

	// We need a bit for restriper to be able to tell when chunks of type
	// SINGLE are available. This "extended" profile format is used in
	// fs_info->avail_*_alloc_bits (in-memory) and balance item fields
	// (on-disk). The corresponding on-disk bit in chunk.type is reserved
	// to avoid remappings between two formats in future.
	availAllocBitSingle = (1 << 48)

	// A fake block group type that is used to communicate global block reserve
	// size to userspace via the SPACE_INFO ioctl.
	spaceInfoGlobalRsv = (1 << 49)

	freeSpaceUsingBitmaps = (1 << 0)

	qgroupLevelShift = 48

	// Is subvolume quota turned on?
	qgroupStatusFlagOn = (1 << 0)
	// RESCAN is set during the initialization phase
	qgroupStatusFlagRescan = (1 << 1)
	// Some qgroup entries are known to be out of date,
	// either because the configuration has changed in a way that
	// makes a rescan necessary, or because the fs has been mounted
	// with a non-qgroup-aware version.
	// Turning qouta off and on again makes it inconsistent, too.
	qgroupStatusFlagInconsistent = (1 << 2)

	qgroupStatusVersion = 1

	// The generation is updated during every commit. As older
	// versions of btrfs are not aware of qgroups, it will be
	// possible to detect inconsistencies by checking the
	// generation on mount time

	// Flag definitions see above

	// Only used during scanning to record the progress
	// of the scan. It contains a logical address

	// Only updated when any of the other values change

)

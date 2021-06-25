package btrfs

/*
 * This header contains the structure definitions and constants used
 * by file system objects that can be retrieved using
 * the _BTRFS_IOC_SEARCH_TREE ioctl.  That means basically anything that
 * is needed to describe a leaf node's key or item contents.
 */

/* holds pointers to all of the tree roots */

/* stores information about which extents are in use, and reference counts */

/*
 * chunk tree stores translations from logical -> physical block numbering
 * the super block points to the chunk tree
 */

/*
 * stores information about which areas of a given device are in use.
 * one per device.  The tree of tree roots points to the device tree
 */

/* one per subvolume, storing files and directories */

/* directory objectid inside the root tree */

/* holds checksums of all the data extents */

/* holds quota configuration and tracking */

/* for storing items that use the _BTRFS_UUID_KEY* types */

/* tracks free space in block groups. */

/* device stats in the device tree */

/* for storing balance parameters in the root tree */

/* orhpan objectid for tracking unlinked/truncated files */

/* does write ahead logging to speed up fsyncs */

/* for space balancing */

/*
 * extent checksums all have this objectid
 * this allows them to share the logging tree
 * for fsyncs
 */

/* For storing free space cache */

/*
 * The inode number assigned to the special inode for storing
 * free ino cache
 */

/* dummy objectid represents multiple objectids */

/*
 * All files have objectids in this range.
 */

/*
 * the device items go into the chunk tree.  The key is in the form
 * [ 1 _BTRFS_DEV_ITEM_KEY device_id ]
 */

/*
 * inode items have the data typically returned from stat and store other
 * info about object characteristics.  There is one for every file and dir in
 * the FS
 */

/* reserve 2-15 close to the inode for later flexibility */

/*
 * dir items are the name -> inode pointers in a directory.  There is one
 * for every name in a directory.
 */

/*
 * extent data is for file data
 */

/*
 * extent csums are stored in a separate tree and hold csums for
 * an entire extent on disk.
 */

/*
 * root items point to tree roots.  They are typically in the root
 * tree used by the super block to find all the other trees
 */

/*
 * root backrefs tie subvols and snapshots to the directory entries that
 * reference them
 */

/*
 * root refs make a fast index for listing all of the snapshots and
 * subvolumes referenced by a given root.  They point directly to the
 * directory item in the root that references the subvol
 */

/*
 * extent items are in the extent map tree.  These record which blocks
 * are used, and how many references there are to each block
 */

/*
 * The same as the _BTRFS_EXTENT_ITEM_KEY, except it's metadata we already know
 * the length, so we save the level in key->offset instead of the length.
 */

/*
 * block groups give us hints into the extent allocation trees.  Which
 * blocks are free etc etc
 */

/*
 * Every block group is represented in the free space tree by a free space info
 * item, which stores some accounting information. It is keyed on
 * (block_group_start, FREE_SPACE_INFO, block_group_length).
 */

/*
 * A free space extent tracks an extent of space that is free in a block group.
 * It is keyed on (start, FREE_SPACE_EXTENT, length).
 */

/*
 * When a block group becomes very fragmented, we convert it to use bitmaps
 * instead of extents. A free space bitmap is keyed on
 * (start, FREE_SPACE_BITMAP, length); the corresponding item is a bitmap with
 * (length / sectorsize) bits.
 */

/*
 * Records the overall state of the qgroups.
 * There's only one instance of this key present,
 * (0, _BTRFS_QGROUP_STATUS_KEY, 0)
 */

/*
 * Records the currently used space of the qgroup.
 * One key per qgroup, (0, _BTRFS_QGROUP_INFO_KEY, qgroupid).
 */

/*
 * Contains the user configured limits for the qgroup.
 * One key per qgroup, (0, _BTRFS_QGROUP_LIMIT_KEY, qgroupid).
 */

/*
 * Records the child-parent relationship of qgroups. For
 * each relation, 2 keys are present:
 * (childid, _BTRFS_QGROUP_RELATION_KEY, parentid)
 * (parentid, _BTRFS_QGROUP_RELATION_KEY, childid)
 */

/*
 * Obsolete name, see _BTRFS_TEMPORARY_ITEM_KEY.
 */

/*
 * The key type for tree items that are stored persistently, but do not need to
 * exist for extended period of time. The items can exist in any tree.
 *
 * [subtype, _BTRFS_TEMPORARY_ITEM_KEY, data]
 *
 * Existing items:
 *
 * - balance status item
 *   (_BTRFS_BALANCE_OBJECTID, _BTRFS_TEMPORARY_ITEM_KEY, 0)
 */

/*
 * Obsolete name, see _BTRFS_PERSISTENT_ITEM_KEY
 */

/*
 * The key type for tree items that are stored persistently and usually exist
 * for a long period, eg. filesystem lifetime. The item kinds can be status
 * information, stats or preference values. The item can exist in any tree.
 *
 * [subtype, _BTRFS_PERSISTENT_ITEM_KEY, data]
 *
 * Existing items:
 *
 * - device statistics, store IO stats in the device tree, one key for all
 *   stats
 *   (_BTRFS_DEV_STATS_OBJECTID, _BTRFS_DEV_STATS_KEY, 0)
 */

/*
 * Persistantly stores the device replace state in the device tree.
 * The key is built like this: (0, _BTRFS_DEV_REPLACE_KEY, 0).
 */

/*
 * Stores items that allow to quickly map UUIDs to something else.
 * These items are part of the filesystem UUID tree.
 * The key is built like this:
 * (UUID_upper_64_bits, _BTRFS_UUID_KEY*, UUID_lower_64_bits).
 */

/* for UUIDs assigned to * received subvols */

/*
 * string items are for debugging.  They just store a short string of
 * data in the FS
 */

/* 32 bytes in various csum fields */

/* csum types */

/*
 * flags definitions for directory entry item type
 *
 * Used by:
 * struct btrfs_dir_item.type
 */

/*
 * The key defines the order in the tree, and so it also defines (optimal)
 * block layout.
 *
 * objectid corresponds to the inode number.
 *
 * type tells us things about the object, and is a kind of stream selector.
 * so for a given inode, keys with type of 1 might refer to the inode data,
 * type of 2 may point to file data in the btree and type == 3 may point to
 * extents.
 *
 * offset is the starting byte offset for this key in the stream.
 *
 * btrfs_disk_key is in disk byte order.  struct btrfs_key is always
 * in cpu native order.  Otherwise they are identical and their sizes
 * should be the same (ie both packed)
 */
type btrfs_disk_key struct {
	objectid uint64
	type_    uint8
	offset   uint64
}

type btrfs_key struct {
	objectid uint64
	type_    uint8
	offset   uint64
}

type btrfs_dev_item struct {
	devid        uint64
	total_bytes  uint64
	bytes_used   uint64
	io_align     uint32
	io_width     uint32
	sector_size  uint32
	type_        uint64
	generation   uint64
	start_offset uint64
	dev_group    uint32
	seek_speed   uint8
	bandwidth    uint8
	uuid         UUID
	fsid         FSID
}

type btrfs_stripe struct {
	devid    uint64
	offset   uint64
	dev_uuid UUID
}

type btrfs_chunk struct {
	length      uint64
	owner       uint64
	stripe_len  uint64
	type_       uint64
	io_align    uint32
	io_width    uint32
	sector_size uint32
	num_stripes uint16
	sub_stripes uint16
	stripe      struct {
		devid    uint64
		offset   uint64
		dev_uuid UUID
	}
}

/* additional stripes go here */
type btrfs_free_space_entry struct {
	offset uint64
	bytes  uint64
	type_  uint8
}

type btrfs_free_space_header struct {
	location struct {
		objectid uint64
		type_    uint8
		offset   uint64
	}
	generation  uint64
	num_entries uint64
	num_bitmaps uint64
}

/* Super block flags */
/* Errors detected */

/*
 * items in the extent btree are used to record the objectid of the
 * owner of the block and the number of references
 */
type btrfs_extent_item struct {
	refs       uint64
	generation uint64
	flags      uint64
}

type btrfs_extent_item_v0 struct {
	refs uint32
}

/* following flags only apply to tree blocks */

/* use full backrefs for extent pointers in the block */

/*
 * this flag is only used internally by scrub and may be changed at any time
 * it is only declared here to avoid collisions
 */
type btrfs_tree_block_info struct {
	key struct {
		objectid uint64
		type_    uint8
		offset   uint64
	}
	level uint8
}

type btrfs_extent_data_ref struct {
	root     uint64
	objectid uint64
	offset   uint64
	count    uint32
}

type btrfs_shared_data_ref struct {
	count uint32
}

type btrfs_extent_inline_ref struct {
	type_  uint8
	offset uint64
}

/* old style backrefs item */
type btrfs_extent_ref_v0 struct {
	root       uint64
	generation uint64
	objectid   uint64
	count      uint32
}

/* dev extents record free space on individual devices.  The owner
 * field points back to the chunk allocation mapping tree that allocated
 * the extent.  The chunk tree uuid field is a way to double check the owner
 */
type btrfs_dev_extent struct {
	chunk_tree      uint64
	chunk_objectid  uint64
	chunk_offset    uint64
	length          uint64
	chunk_tree_uuid UUID
}

type btrfs_inode_ref struct {
	index    uint64
	name_len uint16
}

/* name goes here */
type btrfs_inode_extref struct {
	parent_objectid uint64
	index           uint64
	name_len        uint16
	//name            [0]uint8
}

/* name goes here */
type btrfs_timespec struct {
	sec  uint64
	nsec uint32
}

type btrfs_inode_item struct {
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
	reserved    [4]uint64
	atime       struct {
		sec  uint64
		nsec uint32
	}
	ctime struct {
		sec  uint64
		nsec uint32
	}
	mtime struct {
		sec  uint64
		nsec uint32
	}
	otime struct {
		sec  uint64
		nsec uint32
	}
}

type btrfs_dir_log_item struct {
	end uint64
}

type btrfs_dir_item struct {
	location struct {
		objectid uint64
		type_    uint8
		offset   uint64
	}
	transid  uint64
	data_len uint16
	name_len uint16
	type_    uint8
}

/*
 * Internal in-memory flag that a subvolume has been marked for deletion but
 * still visible as a directory
 */
type btrfs_root_item struct {
	inode struct {
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
		reserved    [4]uint64
		atime       struct {
			sec  uint64
			nsec uint32
		}
		ctime struct {
			sec  uint64
			nsec uint32
		}
		mtime struct {
			sec  uint64
			nsec uint32
		}
		otime struct {
			sec  uint64
			nsec uint32
		}
	}
	generation    uint64
	root_dirid    uint64
	bytenr        uint64
	byte_limit    uint64
	bytes_used    uint64
	last_snapshot uint64
	flags         uint64
	refs          uint32
	drop_progress struct {
		objectid uint64
		type_    uint8
		offset   uint64
	}
	drop_level    uint8
	level         uint8
	generation_v2 uint64
	uuid          UUID
	parent_uuid   UUID
	received_uuid UUID
	ctransid      uint64
	otransid      uint64
	stransid      uint64
	rtransid      uint64
	ctime         struct {
		sec  uint64
		nsec uint32
	}
	otime struct {
		sec  uint64
		nsec uint32
	}
	stime struct {
		sec  uint64
		nsec uint32
	}
	rtime struct {
		sec  uint64
		nsec uint32
	}
	reserved [8]uint64
}

/*
 * this is used for both forward and backward root refs
 */
type btrfs_root_ref struct {
	dirid    uint64
	sequence uint64
	name_len uint16
}

type btrfs_disk_balance_args struct {
	profiles    uint64
	usage       uint64
	usage_min   uint32
	usage_max   uint32
	devid       uint64
	pstart      uint64
	pend        uint64
	vstart      uint64
	vend        uint64
	target      uint64
	flags       uint64
	limit       uint64
	limit_min   uint32
	limit_max   uint32
	stripes_min uint32
	stripes_max uint32
	unused      [6]uint64
}

/*
 * store balance parameters to disk so that balance can be properly
 * resumed after crash or unmount
 */
type btrfs_balance_item struct {
	flags uint64
	data  struct {
		profiles    uint64
		usage       uint64
		usage_min   uint32
		usage_max   uint32
		devid       uint64
		pstart      uint64
		pend        uint64
		vstart      uint64
		vend        uint64
		target      uint64
		flags       uint64
		limit       uint64
		limit_min   uint32
		limit_max   uint32
		stripes_min uint32
		stripes_max uint32
		unused      [6]uint64
	}
	meta struct {
		profiles    uint64
		usage       uint64
		usage_min   uint32
		usage_max   uint32
		devid       uint64
		pstart      uint64
		pend        uint64
		vstart      uint64
		vend        uint64
		target      uint64
		flags       uint64
		limit       uint64
		limit_min   uint32
		limit_max   uint32
		stripes_min uint32
		stripes_max uint32
		unused      [6]uint64
	}
	sys struct {
		profiles    uint64
		usage       uint64
		usage_min   uint32
		usage_max   uint32
		devid       uint64
		pstart      uint64
		pend        uint64
		vstart      uint64
		vend        uint64
		target      uint64
		flags       uint64
		limit       uint64
		limit_min   uint32
		limit_max   uint32
		stripes_min uint32
		stripes_max uint32
		unused      [6]uint64
	}
	unused [4]uint64
}

type btrfs_file_extent_item struct {
	generation     uint64
	ram_bytes      uint64
	compression    uint8
	encryption     uint8
	other_encoding uint16
	type_          uint8
	disk_bytenr    uint64
	disk_num_bytes uint64
	offset         uint64
	num_bytes      uint64
}

type btrfs_csum_item struct {
	csum uint8
}

type btrfs_dev_stats_item struct {
	values [_BTRFS_DEV_STAT_VALUES_MAX]uint64
}

type btrfs_dev_replace_item struct {
	src_devid                     uint64
	cursor_left                   uint64
	cursor_right                  uint64
	cont_reading_from_srcdev_mode uint64
	replace_state                 uint64
	time_started                  uint64
	time_stopped                  uint64
	num_write_errors              uint64
	num_uncorrectable_read_errors uint64
}

/* different types of block groups (and chunks) */
const (
	_BTRFS_RAID_RAID10 = iota
	_BTRFS_RAID_RAID1
	_BTRFS_RAID_DUP
	_BTRFS_RAID_RAID0
	_BTRFS_RAID_SINGLE
	_BTRFS_RAID_RAID5
	_BTRFS_RAID_RAID6
	_BTRFS_NR_RAID_TYPES
)

/*
 * We need a bit for restriper to be able to tell when chunks of type
 * SINGLE are available.  This "extended" profile format is used in
 * fs_info->avail_*_alloc_bits (in-memory) and balance item fields
 * (on-disk).  The corresponding on-disk bit in chunk.type is reserved
 * to avoid remappings between two formats in future.
 */

/*
 * A fake block group type that is used to communicate global block reserve
 * size to userspace via the SPACE_INFO ioctl.
 */
func chunk_to_extended(flags uint64) uint64 {
	if flags&uint64(_BTRFS_BLOCK_GROUP_PROFILE_MASK) == 0 {
		flags |= uint64(availAllocBitSingle)
	}

	return flags
}

func extended_to_chunk(flags uint64) uint64 {
	return flags &^ uint64(availAllocBitSingle)
}

type btrfs_block_group_item struct {
	used           uint64
	chunk_objectid uint64
	flags          uint64
}

type btrfs_free_space_info struct {
	extent_count uint32
	flags        uint32
}

func btrfs_qgroup_level(qgroupid uint64) uint64 {
	return qgroupid >> uint32(qgroupLevelShift)
}

/*
 * is subvolume quota turned on?
 */

/*
 * RESCAN is set during the initialization phase
 */

/*
 * Some qgroup entries are known to be out of date,
 * either because the configuration has changed in a way that
 * makes a rescan necessary, or because the fs has been mounted
 * with a non-qgroup-aware version.
 * Turning qouta off and on again makes it inconsistent, too.
 */
type btrfs_qgroup_status_item struct {
	version    uint64
	generation uint64
	flags      uint64
	rescan     uint64
}

type btrfs_qgroup_info_item struct {
	generation uint64
	rfer       uint64
	rfer_cmpr  uint64
	excl       uint64
	excl_cmpr  uint64
}

type btrfs_qgroup_limit_item struct {
	flags    uint64
	max_rfer uint64
	max_excl uint64
	rsv_rfer uint64
	rsv_excl uint64
}

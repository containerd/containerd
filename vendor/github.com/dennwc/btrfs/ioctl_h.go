package btrfs

import (
	"encoding/binary"
	"encoding/hex"
	"github.com/dennwc/ioctl"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

var order = binary.LittleEndian

const ioctlMagic = 0x94

const devicePathNameMax = 1024

const (
	FSIDSize = 16
	UUIDSize = 16
)

var zeroUUID UUID

type UUID [UUIDSize]byte

func (id UUID) IsZero() bool { return id == zeroUUID }
func (id UUID) String() string {
	if id.IsZero() {
		return "<zero>"
	}
	buf := make([]byte, UUIDSize*2+4)
	i := 0
	i += hex.Encode(buf[i:], id[:4])
	buf[i] = '-'
	i++
	i += hex.Encode(buf[i:], id[4:6])
	buf[i] = '-'
	i++
	i += hex.Encode(buf[i:], id[6:8])
	buf[i] = '-'
	i++
	i += hex.Encode(buf[i:], id[8:10])
	buf[i] = '-'
	i++
	i += hex.Encode(buf[i:], id[10:])
	return string(buf)
}

type FSID [FSIDSize]byte

func (id FSID) String() string { return hex.EncodeToString(id[:]) }

const volNameMax = 4087

// this should be 4k
type btrfs_ioctl_vol_args struct {
	fd   int64
	name [volNameMax + 1]byte
}

func (arg *btrfs_ioctl_vol_args) SetName(name string) {
	n := copy(arg.name[:], name)
	arg.name[n] = 0
}

type btrfs_qgroup_limit struct {
	flags          uint64
	max_referenced uint64
	max_exclusive  uint64
	rsv_referenced uint64
	rsv_exclusive  uint64
}

type btrfs_qgroup_inherit struct {
	flags           uint64
	num_qgroups     uint64
	num_ref_copies  uint64
	num_excl_copies uint64
	lim             btrfs_qgroup_limit
	//qgroups [0]uint64
}

type btrfs_ioctl_qgroup_limit_args struct {
	qgroupid uint64
	lim      btrfs_qgroup_limit
}

type btrfs_ioctl_vol_args_v2_u1 struct {
	size           uint64
	qgroup_inherit *btrfs_qgroup_inherit
}

const subvolNameMax = 4039

type SubvolFlags uint64

func (f SubvolFlags) ReadOnly() bool {
	return f&SubvolReadOnly != 0
}
func (f SubvolFlags) String() string {
	if f == 0 {
		return "<nil>"
	}
	var out []string
	if f&SubvolReadOnly != 0 {
		out = append(out, "RO")
		f = f & (^SubvolReadOnly)
	}
	if f != 0 {
		out = append(out, "0x"+strconv.FormatInt(int64(f), 16))
	}
	return strings.Join(out, "|")
}

// flags for subvolumes
//
// Used by:
// struct btrfs_ioctl_vol_args_v2.flags
//
// BTRFS_SUBVOL_RDONLY is also provided/consumed by the following ioctls:
// - BTRFS_IOC_SUBVOL_GETFLAGS
// - BTRFS_IOC_SUBVOL_SETFLAGS
const (
	subvolCreateAsync   = SubvolFlags(1 << 0)
	SubvolReadOnly      = SubvolFlags(1 << 1)
	subvolQGroupInherit = SubvolFlags(1 << 2)
)

type btrfs_ioctl_vol_args_v2 struct {
	fd      int64
	transid uint64
	flags   SubvolFlags
	btrfs_ioctl_vol_args_v2_u1
	unused [2]uint64
	name   [subvolNameMax + 1]byte
}

// structure to report errors and progress to userspace, either as a
// result of a finished scrub, a canceled scrub or a progress inquiry
type btrfs_scrub_progress struct {
	data_extents_scrubbed uint64 // # of data extents scrubbed
	tree_extents_scrubbed uint64 // # of tree extents scrubbed
	data_bytes_scrubbed   uint64 // # of data bytes scrubbed
	tree_bytes_scrubbed   uint64 // # of tree bytes scrubbed
	read_errors           uint64 // # of read errors encountered (EIO)
	csum_errors           uint64 // # of failed csum checks
	// # of occurences, where the metadata of a tree block did not match the expected values, like generation or logical
	verify_errors uint64
	// # of 4k data block for which no csum is present, probably the result of data written with nodatasum
	no_csum              uint64
	csum_discards        uint64 // # of csum for which no data was found in the extent tree.
	super_errors         uint64 // # of bad super blocks encountered
	malloc_errors        uint64 // # of internal kmalloc errors. These will likely cause an incomplete scrub
	uncorrectable_errors uint64 // # of errors where either no intact copy was found or the writeback failed
	corrected_errors     uint64 // # of errors corrected
	// last physical address scrubbed. In case a scrub was aborted, this can be used to restart the scrub
	last_physical uint64
	// # of occurences where a read for a full (64k) bio failed, but the re-
	// check succeeded for each 4k piece. Intermittent error.
	unverified_errors uint64
}

type btrfs_ioctl_scrub_args struct {
	devid    uint64               // in
	start    uint64               // in
	end      uint64               // in
	flags    uint64               // in
	progress btrfs_scrub_progress // out
	// pad to 1k
	_ [1024 - 4*8 - unsafe.Sizeof(btrfs_scrub_progress{})]byte
}

type contReadingFromSrcdevMode uint64

const (
	_BTRFS_IOCTL_DEV_REPLACE_CONT_READING_FROM_SRCDEV_MODE_ALWAYS contReadingFromSrcdevMode = 0
	_BTRFS_IOCTL_DEV_REPLACE_CONT_READING_FROM_SRCDEV_MODE_AVOID  contReadingFromSrcdevMode = 1
)

type btrfs_ioctl_dev_replace_start_params struct {
	srcdevid                      uint64                      // in, if 0, use srcdev_name instead
	cont_reading_from_srcdev_mode contReadingFromSrcdevMode   // in
	srcdev_name                   [devicePathNameMax + 1]byte // in
	tgtdev_name                   [devicePathNameMax + 1]byte // in
}

type devReplaceState uint64

const (
	_BTRFS_IOCTL_DEV_REPLACE_STATE_NEVER_STARTED devReplaceState = 0
	_BTRFS_IOCTL_DEV_REPLACE_STATE_STARTED       devReplaceState = 1
	_BTRFS_IOCTL_DEV_REPLACE_STATE_FINISHED      devReplaceState = 2
	_BTRFS_IOCTL_DEV_REPLACE_STATE_CANCELED      devReplaceState = 3
	_BTRFS_IOCTL_DEV_REPLACE_STATE_SUSPENDED     devReplaceState = 4
)

type btrfs_ioctl_dev_replace_status_params struct {
	replace_state                 devReplaceState // out
	progress_1000                 uint64          // out, 0 <= x <= 1000
	time_started                  uint64          // out, seconds since 1-Jan-1970
	time_stopped                  uint64          // out, seconds since 1-Jan-1970
	num_write_errors              uint64          // out
	num_uncorrectable_read_errors uint64          // out
}

type btrfs_ioctl_dev_replace_args_u1 struct {
	cmd    uint64                               // in
	result uint64                               // out
	start  btrfs_ioctl_dev_replace_start_params // in
	spare  [64]uint64
}

type btrfs_ioctl_dev_replace_args_u2 struct {
	cmd    uint64                                // in
	result uint64                                // out
	status btrfs_ioctl_dev_replace_status_params // out
	_      [unsafe.Sizeof(btrfs_ioctl_dev_replace_start_params{}) - unsafe.Sizeof(btrfs_ioctl_dev_replace_status_params{})]byte
	spare  [64]uint64
}

type btrfs_ioctl_dev_info_args struct {
	devid       uint64                  // in/out
	uuid        UUID                    // in/out
	bytes_used  uint64                  // out
	total_bytes uint64                  // out
	_           [379]uint64             // pad to 4k
	path        [devicePathNameMax]byte // out
}

type btrfs_ioctl_fs_info_args struct {
	max_id          uint64          // out
	num_devices     uint64          // out
	fsid            FSID            // out
	nodesize        uint32          // out
	sectorsize      uint32          // out
	clone_alignment uint32          // out
	_               [122*8 + 4]byte // pad to 1k
}

type btrfs_ioctl_feature_flags struct {
	compat_flags    FeatureFlags
	compat_ro_flags FeatureFlags
	incompat_flags  IncompatFeatures
}

type argRange [8]byte

func (u argRange) asN() uint64 {
	return order.Uint64(u[:])
}
func (u argRange) asMinMax() (min, max uint32) {
	return order.Uint32(u[:4]), order.Uint32(u[4:])
}

// balance control ioctl modes
//#define BTRFS_BALANCE_CTL_PAUSE		1
//#define BTRFS_BALANCE_CTL_CANCEL	2
//#define BTRFS_BALANCE_CTL_RESUME	3

// this is packed, because it should be exactly the same as its disk
// byte order counterpart (struct btrfs_disk_balance_args)
type btrfs_balance_args struct {
	profiles uint64
	// usage filter
	// BTRFS_BALANCE_ARGS_USAGE with a single value means '0..N'
	// BTRFS_BALANCE_ARGS_USAGE_RANGE - range syntax, min..max
	usage  argRange
	devid  uint64
	pstart uint64
	pend   uint64
	vstart uint64
	vend   uint64
	target uint64
	flags  uint64
	// BTRFS_BALANCE_ARGS_LIMIT with value 'limit' (limit number of processed chunks)
	// BTRFS_BALANCE_ARGS_LIMIT_RANGE - the extend version can use minimum and maximum
	limit       argRange
	stripes_min uint32
	stripes_max uint32
	_           [48]byte
}

// Report balance progress to userspace.
//
// btrfs_balance_progress
type BalanceProgress struct {
	Expected   uint64 // estimated # of chunks that will be relocated to fulfill the request
	Considered uint64 // # of chunks we have considered so far
	Completed  uint64 // # of chunks relocated so far
}

type BalanceState uint64

const (
	BalanceStateRunning   BalanceState = (1 << 0)
	BalanceStatePauseReq  BalanceState = (1 << 1)
	BalanceStateCancelReq BalanceState = (1 << 2)
)

type btrfs_ioctl_balance_args struct {
	flags BalanceFlags       // in/out
	state BalanceState       // out
	data  btrfs_balance_args // in/out
	meta  btrfs_balance_args // in/out
	sys   btrfs_balance_args // in/out
	stat  BalanceProgress    // out
	_     [72 * 8]byte       // pad to 1k
}

const _BTRFS_INO_LOOKUP_PATH_MAX = 4080

type btrfs_ioctl_ino_lookup_args struct {
	treeid   objectID
	objectid objectID
	name     [_BTRFS_INO_LOOKUP_PATH_MAX]byte
}

func (arg *btrfs_ioctl_ino_lookup_args) Name() string {
	n := 0
	for i, b := range arg.name {
		if b == '\x00' {
			n = i
			break
		}
	}
	return string(arg.name[:n])
}

type btrfs_ioctl_search_key struct {
	tree_id objectID // which root are we searching.  0 is the tree of tree roots
	// keys returned will be >= min and <= max
	min_objectid objectID
	max_objectid objectID
	// keys returned will be >= min and <= max
	min_offset uint64
	max_offset uint64
	// max and min transids to search for
	min_transid uint64
	max_transid uint64
	// keys returned will be >= min and <= max
	min_type treeKeyType
	max_type treeKeyType
	// how many items did userland ask for, and how many are we returning
	nr_items uint32
	_        [36]byte
}

type btrfs_ioctl_search_header struct {
	transid  uint64
	objectid objectID
	offset   uint64
	typ      treeKeyType
	len      uint32
}

const _BTRFS_SEARCH_ARGS_BUFSIZE = (4096 - unsafe.Sizeof(btrfs_ioctl_search_key{}))

// the buf is an array of search headers where
// each header is followed by the actual item
// the type field is expanded to 32 bits for alignment
type btrfs_ioctl_search_args struct {
	key btrfs_ioctl_search_key
	buf [_BTRFS_SEARCH_ARGS_BUFSIZE]byte
}

// Extended version of TREE_SEARCH ioctl that can return more than 4k of bytes.
// The allocated size of the buffer is set in buf_size.
type btrfs_ioctl_search_args_v2 struct {
	key      btrfs_ioctl_search_key // in/out - search parameters
	buf_size uint64                 // in - size of buffer; out - on EOVERFLOW: needed size to store item
	//buf      [0]uint64 // out - found items
}

// With a @src_length of zero, the range from @src_offset->EOF is cloned!
type btrfs_ioctl_clone_range_args struct {
	src_fd      int64
	src_offset  uint64
	src_length  uint64
	dest_offset uint64
}

// flags for the defrag range ioctl
type defragRange uint64

const (
	_BTRFS_DEFRAG_RANGE_COMPRESS defragRange = 1
	_BTRFS_DEFRAG_RANGE_START_IO defragRange = 2
)

const _BTRFS_SAME_DATA_DIFFERS = 1

// For extent-same ioctl
type btrfs_ioctl_same_extent_info struct {
	fd             int64  // in - destination file
	logical_offset uint64 // in - start of extent in destination
	bytes_deduped  uint64 // out - total # of bytes we were able to dedupe from this file
	// out; status of this dedupe operation:
	// 0 if dedup succeeds
	// < 0 for error
	// == BTRFS_SAME_DATA_DIFFERS if data differs
	status   int32
	reserved uint32
}

type btrfs_ioctl_same_args struct {
	logical_offset uint64 // in - start of extent in source
	length         uint64 // in - length of extent
	dest_count     uint16 // in - total elements in info array
	_              [6]byte
	//info           [0]btrfs_ioctl_same_extent_info
}

type btrfs_ioctl_defrag_range_args struct {
	start uint64 // start of the defrag operation
	len   uint64 // number of bytes to defrag, use (u64)-1 to say all
	// flags for the operation, which can include turning
	// on compression for this one defrag
	flags uint64
	// any extent bigger than this will be considered
	// already defragged.  Use 0 to take the kernel default
	// Use 1 to say every single extent must be rewritten
	extent_thresh uint32
	// which compression method to use if turning on compression
	// for this defrag operation.  If unspecified, zlib will be used
	compress_type uint32
	_             [16]byte // spare for later
}

type btrfs_ioctl_space_info struct {
	flags       uint64
	total_bytes uint64
	used_bytes  uint64
}

type btrfs_ioctl_space_args struct {
	space_slots  uint64
	total_spaces uint64
	//spaces       [0]btrfs_ioctl_space_info
}

type btrfs_data_container struct {
	bytes_left    uint32 // out -- bytes not needed to deliver output
	bytes_missing uint32 // out -- additional bytes needed for result
	elem_cnt      uint32 // out
	elem_missed   uint32 // out
	//val           [0]uint64
}

type btrfs_ioctl_ino_path_args struct {
	inum uint64 // in
	size uint64 // in
	_    [32]byte
	// struct btrfs_data_container	*fspath;	   out
	fspath uint64 // out
}

type btrfs_ioctl_logical_ino_args struct {
	logical uint64 // in
	size    uint64 // in
	_       [32]byte
	// struct btrfs_data_container	*inodes;	out
	inodes uint64
}

// disk I/O failure stats
const (
	_BTRFS_DEV_STAT_WRITE_ERRS = iota // EIO or EREMOTEIO from lower layers
	_BTRFS_DEV_STAT_READ_ERRS         // EIO or EREMOTEIO from lower layers
	_BTRFS_DEV_STAT_FLUSH_ERRS        // EIO or EREMOTEIO from lower layers

	// stats for indirect indications for I/O failures

	// checksum error, bytenr error or contents is illegal: this is an
	// indication that the block was damaged during read or write, or written to
	// wrong location or read from wrong location
	_BTRFS_DEV_STAT_CORRUPTION_ERRS
	_BTRFS_DEV_STAT_GENERATION_ERRS // an indication that blocks have not been written

	_BTRFS_DEV_STAT_VALUES_MAX
)

// Reset statistics after reading; needs SYS_ADMIN capability
const _BTRFS_DEV_STATS_RESET = (1 << 0)

type btrfs_ioctl_get_dev_stats struct {
	devid    uint64                                       // in
	nr_items uint64                                       // in/out
	flags    uint64                                       // in/out
	values   [_BTRFS_DEV_STAT_VALUES_MAX]uint64           // out values
	_        [128 - 2 - _BTRFS_DEV_STAT_VALUES_MAX]uint64 // pad to 1k
}

const (
	_BTRFS_QUOTA_CTL_ENABLE  = 1
	_BTRFS_QUOTA_CTL_DISABLE = 2
	// 3 has formerly been reserved for BTRFS_QUOTA_CTL_RESCAN
)

type btrfs_ioctl_quota_ctl_args struct {
	cmd    uint64
	status uint64
}

type btrfs_ioctl_quota_rescan_args struct {
	flags    uint64
	progress uint64
	_        [6]uint64
}

type btrfs_ioctl_qgroup_assign_args struct {
	assign uint64
	src    uint64
	dst    uint64
}

type btrfs_ioctl_qgroup_create_args struct {
	create   uint64
	qgroupid uint64
}

type btrfs_ioctl_timespec struct {
	sec  uint64
	nsec uint32
}

type btrfs_ioctl_received_subvol_args struct {
	uuid     UUID                 // in
	stransid uint64               // in
	rtransid uint64               // out
	stime    btrfs_ioctl_timespec // in
	rtime    btrfs_ioctl_timespec // out
	flags    uint64               // in
	_        [16]uint64           // in
}

const (
	// Caller doesn't want file data in the send stream, even if the
	// search of clone sources doesn't find an extent. UPDATE_EXTENT
	// commands will be sent instead of WRITE commands.
	_BTRFS_SEND_FLAG_NO_FILE_DATA = 0x1
	// Do not add the leading stream header. Used when multiple snapshots
	// are sent back to back.
	_BTRFS_SEND_FLAG_OMIT_STREAM_HEADER = 0x2
	// Omit the command at the end of the stream that indicated the end
	// of the stream. This option is used when multiple snapshots are
	// sent back to back.
	_BTRFS_SEND_FLAG_OMIT_END_CMD = 0x4

	_BTRFS_SEND_FLAG_MASK = _BTRFS_SEND_FLAG_NO_FILE_DATA |
		_BTRFS_SEND_FLAG_OMIT_STREAM_HEADER |
		_BTRFS_SEND_FLAG_OMIT_END_CMD
)

type btrfs_ioctl_send_args struct {
	send_fd             int64     // in
	clone_sources_count uint64    // in
	clone_sources       *objectID // in
	parent_root         objectID  // in
	flags               uint64    // in
	_                   [4]uint64 // in
}

var (
	_BTRFS_IOC_SNAP_CREATE            = ioctl.IOW(ioctlMagic, 1, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_DEFRAG                 = ioctl.IOW(ioctlMagic, 2, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_RESIZE                 = ioctl.IOW(ioctlMagic, 3, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_SCAN_DEV               = ioctl.IOW(ioctlMagic, 4, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_TRANS_START            = ioctl.IO(ioctlMagic, 6)
	_BTRFS_IOC_TRANS_END              = ioctl.IO(ioctlMagic, 7)
	_BTRFS_IOC_SYNC                   = ioctl.IO(ioctlMagic, 8)
	_BTRFS_IOC_CLONE                  = ioctl.IOW(ioctlMagic, 9, 4) // int32
	_BTRFS_IOC_ADD_DEV                = ioctl.IOW(ioctlMagic, 10, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_RM_DEV                 = ioctl.IOW(ioctlMagic, 11, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_BALANCE                = ioctl.IOW(ioctlMagic, 12, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_CLONE_RANGE            = ioctl.IOW(ioctlMagic, 13, unsafe.Sizeof(btrfs_ioctl_clone_range_args{}))
	_BTRFS_IOC_SUBVOL_CREATE          = ioctl.IOW(ioctlMagic, 14, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_SNAP_DESTROY           = ioctl.IOW(ioctlMagic, 15, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_DEFRAG_RANGE           = ioctl.IOW(ioctlMagic, 16, unsafe.Sizeof(btrfs_ioctl_defrag_range_args{}))
	_BTRFS_IOC_TREE_SEARCH            = ioctl.IOWR(ioctlMagic, 17, unsafe.Sizeof(btrfs_ioctl_search_args{}))
	_BTRFS_IOC_INO_LOOKUP             = ioctl.IOWR(ioctlMagic, 18, unsafe.Sizeof(btrfs_ioctl_ino_lookup_args{}))
	_BTRFS_IOC_DEFAULT_SUBVOL         = ioctl.IOW(ioctlMagic, 19, 8) // uint64
	_BTRFS_IOC_SPACE_INFO             = ioctl.IOWR(ioctlMagic, 20, unsafe.Sizeof(btrfs_ioctl_space_args{}))
	_BTRFS_IOC_START_SYNC             = ioctl.IOR(ioctlMagic, 24, 8) // uint64
	_BTRFS_IOC_WAIT_SYNC              = ioctl.IOW(ioctlMagic, 22, 8) // uint64
	_BTRFS_IOC_SNAP_CREATE_V2         = ioctl.IOW(ioctlMagic, 23, unsafe.Sizeof(btrfs_ioctl_vol_args_v2{}))
	_BTRFS_IOC_SUBVOL_CREATE_V2       = ioctl.IOW(ioctlMagic, 24, unsafe.Sizeof(btrfs_ioctl_vol_args_v2{}))
	_BTRFS_IOC_SUBVOL_GETFLAGS        = ioctl.IOR(ioctlMagic, 25, 8) // uint64
	_BTRFS_IOC_SUBVOL_SETFLAGS        = ioctl.IOW(ioctlMagic, 26, 8) // uint64
	_BTRFS_IOC_SCRUB                  = ioctl.IOWR(ioctlMagic, 27, unsafe.Sizeof(btrfs_ioctl_scrub_args{}))
	_BTRFS_IOC_SCRUB_CANCEL           = ioctl.IO(ioctlMagic, 28)
	_BTRFS_IOC_SCRUB_PROGRESS         = ioctl.IOWR(ioctlMagic, 29, unsafe.Sizeof(btrfs_ioctl_scrub_args{}))
	_BTRFS_IOC_DEV_INFO               = ioctl.IOWR(ioctlMagic, 30, unsafe.Sizeof(btrfs_ioctl_dev_info_args{}))
	_BTRFS_IOC_FS_INFO                = ioctl.IOR(ioctlMagic, 31, unsafe.Sizeof(btrfs_ioctl_fs_info_args{}))
	_BTRFS_IOC_BALANCE_V2             = ioctl.IOWR(ioctlMagic, 32, unsafe.Sizeof(btrfs_ioctl_balance_args{}))
	_BTRFS_IOC_BALANCE_CTL            = ioctl.IOW(ioctlMagic, 33, 4) // int32
	_BTRFS_IOC_BALANCE_PROGRESS       = ioctl.IOR(ioctlMagic, 34, unsafe.Sizeof(btrfs_ioctl_balance_args{}))
	_BTRFS_IOC_INO_PATHS              = ioctl.IOWR(ioctlMagic, 35, unsafe.Sizeof(btrfs_ioctl_ino_path_args{}))
	_BTRFS_IOC_LOGICAL_INO            = ioctl.IOWR(ioctlMagic, 36, unsafe.Sizeof(btrfs_ioctl_ino_path_args{}))
	_BTRFS_IOC_SET_RECEIVED_SUBVOL    = ioctl.IOWR(ioctlMagic, 37, unsafe.Sizeof(btrfs_ioctl_received_subvol_args{}))
	_BTRFS_IOC_SEND                   = ioctl.IOW(ioctlMagic, 38, unsafe.Sizeof(btrfs_ioctl_send_args{}))
	_BTRFS_IOC_DEVICES_READY          = ioctl.IOR(ioctlMagic, 39, unsafe.Sizeof(btrfs_ioctl_vol_args{}))
	_BTRFS_IOC_QUOTA_CTL              = ioctl.IOWR(ioctlMagic, 40, unsafe.Sizeof(btrfs_ioctl_quota_ctl_args{}))
	_BTRFS_IOC_QGROUP_ASSIGN          = ioctl.IOW(ioctlMagic, 41, unsafe.Sizeof(btrfs_ioctl_qgroup_assign_args{}))
	_BTRFS_IOC_QGROUP_CREATE          = ioctl.IOW(ioctlMagic, 42, unsafe.Sizeof(btrfs_ioctl_qgroup_create_args{}))
	_BTRFS_IOC_QGROUP_LIMIT           = ioctl.IOR(ioctlMagic, 43, unsafe.Sizeof(btrfs_ioctl_qgroup_limit_args{}))
	_BTRFS_IOC_QUOTA_RESCAN           = ioctl.IOW(ioctlMagic, 44, unsafe.Sizeof(btrfs_ioctl_quota_rescan_args{}))
	_BTRFS_IOC_QUOTA_RESCAN_STATUS    = ioctl.IOR(ioctlMagic, 45, unsafe.Sizeof(btrfs_ioctl_quota_rescan_args{}))
	_BTRFS_IOC_QUOTA_RESCAN_WAIT      = ioctl.IO(ioctlMagic, 46)
	_BTRFS_IOC_GET_FSLABEL            = ioctl.IOR(ioctlMagic, 49, labelSize)
	_BTRFS_IOC_SET_FSLABEL            = ioctl.IOW(ioctlMagic, 50, labelSize)
	_BTRFS_IOC_GET_DEV_STATS          = ioctl.IOWR(ioctlMagic, 52, unsafe.Sizeof(btrfs_ioctl_get_dev_stats{}))
	_BTRFS_IOC_DEV_REPLACE            = ioctl.IOWR(ioctlMagic, 53, unsafe.Sizeof(btrfs_ioctl_dev_replace_args_u1{}))
	_BTRFS_IOC_FILE_EXTENT_SAME       = ioctl.IOWR(ioctlMagic, 54, unsafe.Sizeof(btrfs_ioctl_same_args{}))
	_BTRFS_IOC_GET_FEATURES           = ioctl.IOR(ioctlMagic, 57, unsafe.Sizeof(btrfs_ioctl_feature_flags{}))
	_BTRFS_IOC_SET_FEATURES           = ioctl.IOW(ioctlMagic, 57, unsafe.Sizeof([2]btrfs_ioctl_feature_flags{}))
	_BTRFS_IOC_GET_SUPPORTED_FEATURES = ioctl.IOR(ioctlMagic, 57, unsafe.Sizeof([3]btrfs_ioctl_feature_flags{}))
)

func iocSnapCreate(f *os.File, in *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SNAP_CREATE, in)
}

func iocSnapCreateV2(f *os.File, in *btrfs_ioctl_vol_args_v2) error {
	return ioctl.Do(f, _BTRFS_IOC_SNAP_CREATE_V2, in)
}

func iocDefrag(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_DEFRAG, out)
}

func iocResize(f *os.File, in *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_RESIZE, in)
}

func iocScanDev(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SCAN_DEV, out)
}

func iocTransStart(f *os.File) error {
	return ioctl.Do(f, _BTRFS_IOC_TRANS_START, nil)
}

func iocTransEnd(f *os.File) error {
	return ioctl.Do(f, _BTRFS_IOC_TRANS_END, nil)
}

func iocSync(f *os.File) error {
	return ioctl.Do(f, _BTRFS_IOC_SYNC, nil)
}

func iocClone(dst, src *os.File) error {
	return ioctl.Ioctl(dst, _BTRFS_IOC_CLONE, src.Fd())
}

func iocAddDev(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_ADD_DEV, out)
}

func iocRmDev(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_RM_DEV, out)
}

func iocBalance(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_BALANCE, out)
}

func iocCloneRange(f *os.File, out *btrfs_ioctl_clone_range_args) error {
	return ioctl.Do(f, _BTRFS_IOC_CLONE_RANGE, out)
}

func iocSubvolCreate(f *os.File, in *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SUBVOL_CREATE, in)
}

func iocSubvolCreateV2(f *os.File, in *btrfs_ioctl_vol_args_v2) error {
	return ioctl.Do(f, _BTRFS_IOC_SUBVOL_CREATE, in)
}

func iocSnapDestroy(f *os.File, in *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SNAP_DESTROY, in)
}

func iocDefragRange(f *os.File, out *btrfs_ioctl_defrag_range_args) error {
	return ioctl.Do(f, _BTRFS_IOC_DEFRAG_RANGE, out)
}

func iocTreeSearch(f *os.File, out *btrfs_ioctl_search_args) error {
	return ioctl.Do(f, _BTRFS_IOC_TREE_SEARCH, out)
}

func iocInoLookup(f *os.File, out *btrfs_ioctl_ino_lookup_args) error {
	return ioctl.Do(f, _BTRFS_IOC_INO_LOOKUP, out)
}

func iocDefaultSubvol(f *os.File, out *uint64) error {
	return ioctl.Do(f, _BTRFS_IOC_DEFAULT_SUBVOL, out)
}

type spaceFlags uint64

func (f spaceFlags) BlockGroup() blockGroup {
	return blockGroup(f) & _BTRFS_BLOCK_GROUP_MASK
}

type spaceInfo struct {
	Flags      spaceFlags
	TotalBytes uint64
	UsedBytes  uint64
}

func iocSpaceInfo(f *os.File) ([]spaceInfo, error) {
	arg := &btrfs_ioctl_space_args{}
	if err := ioctl.Do(f, _BTRFS_IOC_SPACE_INFO, arg); err != nil {
		return nil, err
	}
	n := arg.total_spaces
	if n == 0 {
		return nil, nil
	}
	const (
		argSize  = unsafe.Sizeof(btrfs_ioctl_space_args{})
		infoSize = unsafe.Sizeof(btrfs_ioctl_space_info{})
	)
	buf := make([]byte, argSize+uintptr(n)*infoSize)
	basePtr := unsafe.Pointer(&buf[0])
	arg = (*btrfs_ioctl_space_args)(basePtr)
	arg.space_slots = n
	if err := ioctl.Do(f, _BTRFS_IOC_SPACE_INFO, arg); err != nil {
		return nil, err
	} else if arg.total_spaces == 0 {
		return nil, nil
	}
	if n > arg.total_spaces {
		n = arg.total_spaces
	}
	out := make([]spaceInfo, n)
	ptr := uintptr(basePtr) + argSize
	for i := 0; i < int(n); i++ {
		info := (*btrfs_ioctl_space_info)(unsafe.Pointer(ptr))
		out[i] = spaceInfo{
			Flags:      spaceFlags(info.flags),
			TotalBytes: info.total_bytes,
			UsedBytes:  info.used_bytes,
		}
		ptr += infoSize
	}
	return out, nil
}

func iocStartSync(f *os.File, out *uint64) error {
	return ioctl.Do(f, _BTRFS_IOC_START_SYNC, out)
}

func iocWaitSync(f *os.File, out *uint64) error {
	return ioctl.Do(f, _BTRFS_IOC_WAIT_SYNC, out)
}

func iocSubvolGetflags(f *os.File) (out SubvolFlags, err error) {
	err = ioctl.Do(f, _BTRFS_IOC_SUBVOL_GETFLAGS, &out)
	return
}

func iocSubvolSetflags(f *os.File, flags SubvolFlags) error {
	v := uint64(flags)
	return ioctl.Do(f, _BTRFS_IOC_SUBVOL_SETFLAGS, &v)
}

func iocScrub(f *os.File, out *btrfs_ioctl_scrub_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SCRUB, out)
}

func iocScrubCancel(f *os.File) error {
	return ioctl.Do(f, _BTRFS_IOC_SCRUB_CANCEL, nil)
}

func iocScrubProgress(f *os.File, out *btrfs_ioctl_scrub_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SCRUB_PROGRESS, out)
}

func iocFsInfo(f *os.File) (out btrfs_ioctl_fs_info_args, err error) {
	err = ioctl.Do(f, _BTRFS_IOC_FS_INFO, &out)
	return
}

func iocDevInfo(f *os.File, devid uint64, uuid UUID) (out btrfs_ioctl_dev_info_args, err error) {
	out.devid = devid
	out.uuid = uuid
	err = ioctl.Do(f, _BTRFS_IOC_DEV_INFO, &out)
	return
}

func iocBalanceV2(f *os.File, out *btrfs_ioctl_balance_args) error {
	return ioctl.Do(f, _BTRFS_IOC_BALANCE_V2, out)
}

func iocBalanceCtl(f *os.File, out *int32) error {
	return ioctl.Do(f, _BTRFS_IOC_BALANCE_CTL, out)
}

func iocBalanceProgress(f *os.File, out *btrfs_ioctl_balance_args) error {
	return ioctl.Do(f, _BTRFS_IOC_BALANCE_PROGRESS, out)
}

func iocInoPaths(f *os.File, out *btrfs_ioctl_ino_path_args) error {
	return ioctl.Do(f, _BTRFS_IOC_INO_PATHS, out)
}

func iocLogicalIno(f *os.File, out *btrfs_ioctl_ino_path_args) error {
	return ioctl.Do(f, _BTRFS_IOC_LOGICAL_INO, out)
}

func iocSetReceivedSubvol(f *os.File, out *btrfs_ioctl_received_subvol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SET_RECEIVED_SUBVOL, out)
}

func iocSend(f *os.File, in *btrfs_ioctl_send_args) error {
	return ioctl.Do(f, _BTRFS_IOC_SEND, in)
}

func iocDevicesReady(f *os.File, out *btrfs_ioctl_vol_args) error {
	return ioctl.Do(f, _BTRFS_IOC_DEVICES_READY, out)
}

func iocQuotaCtl(f *os.File, out *btrfs_ioctl_quota_ctl_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QUOTA_CTL, out)
}

func iocQgroupAssign(f *os.File, out *btrfs_ioctl_qgroup_assign_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QGROUP_ASSIGN, out)
}

func iocQgroupCreate(f *os.File, out *btrfs_ioctl_qgroup_create_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QGROUP_CREATE, out)
}

func iocQgroupLimit(f *os.File, out *btrfs_ioctl_qgroup_limit_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QGROUP_LIMIT, out)
}

func iocQuotaRescan(f *os.File, out *btrfs_ioctl_quota_rescan_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QUOTA_RESCAN, out)
}

func iocQuotaRescanStatus(f *os.File, out *btrfs_ioctl_quota_rescan_args) error {
	return ioctl.Do(f, _BTRFS_IOC_QUOTA_RESCAN_STATUS, out)
}

func iocQuotaRescanWait(f *os.File) error {
	return ioctl.Do(f, _BTRFS_IOC_QUOTA_RESCAN_WAIT, nil)
}

func iocGetFslabel(f *os.File, out *[labelSize]byte) error {
	return ioctl.Do(f, _BTRFS_IOC_GET_FSLABEL, out)
}

func iocSetFslabel(f *os.File, out *[labelSize]byte) error {
	return ioctl.Do(f, _BTRFS_IOC_SET_FSLABEL, out)
}

func iocGetDevStats(f *os.File, out *btrfs_ioctl_get_dev_stats) error {
	return ioctl.Do(f, _BTRFS_IOC_GET_DEV_STATS, out)
}

//func iocDevReplace(f *os.File, out *btrfs_ioctl_dev_replace_args) error {
//	return ioctl.Do(f, _BTRFS_IOC_DEV_REPLACE, out)
//}

func iocFileExtentSame(f *os.File, out *btrfs_ioctl_same_args) error {
	return ioctl.Do(f, _BTRFS_IOC_FILE_EXTENT_SAME, out)
}

func iocSetFeatures(f *os.File, out *[2]btrfs_ioctl_feature_flags) error {
	return ioctl.Do(f, _BTRFS_IOC_SET_FEATURES, out)
}

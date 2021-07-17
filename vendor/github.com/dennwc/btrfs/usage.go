package btrfs

import (
	"os"
	"sort"
	"syscall"
)

func cmpChunkBlockGroup(f1, f2 blockGroup) int {
	var mask blockGroup

	if (f1 & _BTRFS_BLOCK_GROUP_TYPE_MASK) ==
		(f2 & _BTRFS_BLOCK_GROUP_TYPE_MASK) {
		mask = _BTRFS_BLOCK_GROUP_PROFILE_MASK
	} else if f2&blockGroupSystem != 0 {
		return -1
	} else if f1&blockGroupSystem != 0 {
		return +1
	} else {
		mask = _BTRFS_BLOCK_GROUP_TYPE_MASK
	}

	if (f1 & mask) > (f2 & mask) {
		return +1
	} else if (f1 & mask) < (f2 & mask) {
		return -1
	} else {
		return 0
	}
}

type spaceInfoByBlockGroup []spaceInfo

func (a spaceInfoByBlockGroup) Len() int      { return len(a) }
func (a spaceInfoByBlockGroup) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a spaceInfoByBlockGroup) Less(i, j int) bool {
	return cmpChunkBlockGroup(blockGroup(a[i].Flags), blockGroup(a[j].Flags)) < 0
}

type UsageInfo struct {
	Total       uint64
	TotalUnused uint64
	TotalUsed   uint64
	TotalChunks uint64

	FreeEstimated uint64
	FreeMin       uint64

	LogicalDataChunks uint64
	RawDataChunks     uint64
	RawDataUsed       uint64

	LogicalMetaChunks uint64
	RawMetaChunks     uint64
	RawMetaUsed       uint64

	SystemUsed   uint64
	SystemChunks uint64

	DataRatio     float64
	MetadataRatio float64

	GlobalReserve     uint64
	GlobalReserveUsed uint64
}

const minUnallocatedThreshold = 16 * 1024 * 1024

func spaceUsage(f *os.File) (UsageInfo, error) {
	info, err := iocFsInfo(f)
	if err != nil {
		return UsageInfo{}, err
	}
	var u UsageInfo
	for i := uint64(0); i <= info.max_id; i++ {
		dev, err := iocDevInfo(f, i, UUID{})
		if err == syscall.ENODEV {
			continue
		} else if err != nil {
			return UsageInfo{}, err
		}
		u.Total += dev.total_bytes
	}

	spaces, err := iocSpaceInfo(f)
	if err != nil {
		return UsageInfo{}, err
	}
	sort.Sort(spaceInfoByBlockGroup(spaces))
	var (
		maxDataRatio int = 1
		mixed        bool
	)
	for _, s := range spaces {
		ratio := 1
		bg := s.Flags.BlockGroup()
		switch {
		case bg&blockGroupRaid0 != 0:
			ratio = 1
		case bg&blockGroupRaid1 != 0:
			ratio = 2
		case bg&blockGroupRaid5 != 0:
			ratio = 0
		case bg&blockGroupRaid6 != 0:
			ratio = 0
		case bg&blockGroupDup != 0:
			ratio = 2
		case bg&blockGroupRaid10 != 0:
			ratio = 2
		}
		if ratio > maxDataRatio {
			maxDataRatio = ratio
		}
		if bg&spaceInfoGlobalRsv != 0 {
			u.GlobalReserve = s.TotalBytes
			u.GlobalReserveUsed = s.UsedBytes
		}
		if bg&(blockGroupData|blockGroupMetadata) == (blockGroupData | blockGroupMetadata) {
			mixed = true
		}
		if bg&blockGroupData != 0 {
			u.RawDataUsed += s.UsedBytes * uint64(ratio)
			u.RawDataChunks += s.TotalBytes * uint64(ratio)
			u.LogicalDataChunks += s.TotalBytes
		}
		if bg&blockGroupMetadata != 0 {
			u.RawMetaUsed += s.UsedBytes * uint64(ratio)
			u.RawMetaChunks += s.TotalBytes * uint64(ratio)
			u.LogicalMetaChunks += s.TotalBytes
		}
		if bg&blockGroupSystem != 0 {
			u.SystemUsed += s.UsedBytes * uint64(ratio)
			u.SystemChunks += s.TotalBytes * uint64(ratio)
		}
	}
	u.TotalChunks = u.RawDataChunks + u.SystemChunks
	u.TotalUsed = u.RawDataUsed + u.SystemUsed
	if !mixed {
		u.TotalChunks += u.RawMetaChunks
		u.TotalUsed += u.RawMetaUsed
	}
	u.TotalUnused = u.Total - u.TotalChunks

	u.DataRatio = float64(u.RawDataChunks) / float64(u.LogicalDataChunks)
	if mixed {
		u.MetadataRatio = u.DataRatio
	} else {
		u.MetadataRatio = float64(u.RawMetaChunks) / float64(u.LogicalMetaChunks)
	}

	// We're able to fill at least DATA for the unused space
	//
	// With mixed raid levels, this gives a rough estimate but more
	// accurate than just counting the logical free space
	// (l_data_chunks - l_data_used)
	//
	// In non-mixed case there's no difference.
	u.FreeEstimated = uint64(float64(u.RawDataChunks-u.RawDataUsed) / u.DataRatio)

	// For mixed-bg the metadata are left out in calculations thus global
	// reserve would be lost. Part of it could be permanently allocated,
	// we have to subtract the used bytes so we don't go under zero free.
	if mixed {
		u.FreeEstimated -= u.GlobalReserve - u.GlobalReserveUsed
	}
	u.FreeMin = u.FreeEstimated

	// Chop unallocatable space
	// FIXME: must be applied per device
	if u.TotalUnused >= minUnallocatedThreshold {
		u.FreeEstimated += uint64(float64(u.TotalUnused) / u.DataRatio)
		// Match the calculation of 'df', use the highest raid ratio
		u.FreeMin += u.TotalUnused / uint64(maxDataRatio)
	}
	return u, nil
}

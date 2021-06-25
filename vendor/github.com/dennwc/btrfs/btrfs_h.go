package btrfs

import "strings"

const maxUint64 = 1<<64 - 1

const labelSize = 256

type FeatureFlags uint64

const (
	FeatureCompatROFreeSpaceTree = FeatureFlags(1 << 0)
)

type IncompatFeatures uint64

func (f IncompatFeatures) String() string {
	var s []string
	for i, name := range incompatFeatureNames {
		if uint64(f)&uint64(i) != 0 {
			s = append(s, name)
		}
	}
	return strings.Join(s, ",")
}

var incompatFeatureNames = []string{
	"DefaultSubvol",
	"MixedGroups",
	"CompressLZO",
	"CompressLZOv2",
	"BigMetadata",
	"ExtendedIRef",
	"RAID56",
	"SkinnyMetadata",
	"NoHoles",
}

const (
	FeatureIncompatMixedBackRef  = IncompatFeatures(1 << 0)
	FeatureIncompatDefaultSubvol = IncompatFeatures(1 << 1)
	FeatureIncompatMixedGroups   = IncompatFeatures(1 << 2)
	FeatureIncompatCompressLZO   = IncompatFeatures(1 << 3)

	// Some patches floated around with a second compression method
	// lets save that incompat here for when they do get in.
	// Note we don't actually support it, we're just reserving the number.
	FeatureIncompatCompressLZOv2 = IncompatFeatures(1 << 4)

	// Older kernels tried to do bigger metadata blocks, but the
	// code was pretty buggy. Lets not let them try anymore.
	FeatureIncompatBigMetadata = IncompatFeatures(1 << 5)

	FeatureIncompatExtendedIRef   = IncompatFeatures(1 << 6)
	FeatureIncompatRAID56         = IncompatFeatures(1 << 7)
	FeatureIncompatSkinnyMetadata = IncompatFeatures(1 << 8)
	FeatureIncompatNoHoles        = IncompatFeatures(1 << 9)
)

// Flags definition for balance.
type BalanceFlags uint64

// Restriper's general type filter.
const (
	BalanceData     = BalanceFlags(1 << 0)
	BalanceSystem   = BalanceFlags(1 << 1)
	BalanceMetadata = BalanceFlags(1 << 2)

	BalanceMask = (BalanceData | BalanceSystem | BalanceMetadata)

	BalanceForce  = BalanceFlags(1 << 3)
	BalanceResume = BalanceFlags(1 << 4)
)

package btrfs

import (
	"errors"
	"fmt"
)

type ErrNotBtrfs struct {
	Path string
}

func (e ErrNotBtrfs) Error() string {
	return fmt.Sprintf("not a btrfs filesystem: %s", e.Path)
}

// Error codes as returned by the kernel
type ErrCode int

func (e ErrCode) Error() string {
	s, ok := errorString[e]
	if ok {
		return s
	}
	return fmt.Sprintf("error %d", int(e))
}

const (
	ErrDevRAID1MinNotMet = ErrCode(iota + 1)
	ErrDevRAID10MinNotMet
	ErrDevRAID5MinNotMet
	ErrDevRAID6MinNotMet
	ErrDevTargetReplace
	ErrDevMissingNotFound
	ErrDevOnlyWritable
	ErrDevExclRunInProgress
)

var errorString = map[ErrCode]string{
	ErrDevRAID1MinNotMet:    "unable to go below two devices on raid1",
	ErrDevRAID10MinNotMet:   "unable to go below four devices on raid10",
	ErrDevRAID5MinNotMet:    "unable to go below two devices on raid5",
	ErrDevRAID6MinNotMet:    "unable to go below three devices on raid6",
	ErrDevTargetReplace:     "unable to remove the dev_replace target dev",
	ErrDevMissingNotFound:   "no missing devices found to remove",
	ErrDevOnlyWritable:      "unable to remove the only writeable device",
	ErrDevExclRunInProgress: "add/delete/balance/replace/resize operation in progress",
}

var (
	ErrNotFound       = errors.New("not found")
	errNotImplemented = errors.New("not implemented")
)

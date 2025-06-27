package fuse

import "syscall"

const (
	ENOATTR = Status(syscall.ENOATTR)
	ENODATA = Status(syscall.EIO)
)

const (
	CAP_NO_OPENDIR_SUPPORT = (1 << 24)

	// The higher capabilities are not currently defined by FreeBSD.
	// Ref: https://cgit.freebsd.org/src/tree/sys/fs/fuse/fuse_kernel.h

	// CAP_RENAME_SWAP only exists on OSX.
	CAP_RENAME_SWAP = 0x0

	// CAP_EXPLICIT_INVAL_DATA is not supported on FreeBSD.
	CAP_EXPLICIT_INVAL_DATA = 0x0
)

func (s *StatfsOut) FromStatfsT(statfs *syscall.Statfs_t) {
	s.Blocks = statfs.Blocks
	s.Bsize = uint32(statfs.Bsize)
	s.Bfree = statfs.Bfree
	s.Bavail = uint64(statfs.Bavail)
	s.Files = statfs.Files
	s.Ffree = uint64(statfs.Ffree)
	s.Frsize = uint32(statfs.Bsize)
	s.NameLen = uint32(statfs.Namemax)
}

func (o *InitOut) setFlags(flags uint64) {
	o.Flags = uint32(flags)
}

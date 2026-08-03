package erofs

import (
	"io/fs"
	"syscall"

	"github.com/erofs/go-erofs/internal/builder"
)

func entryFromSys(info fs.FileInfo) *builder.Entry {
	switch sys := info.Sys().(type) {
	case *builder.Entry:
		return sys
	case *syscall.Stat_t:
		return &builder.Entry{
			UID:     sys.Uid,
			GID:     sys.Gid,
			Mtime:   uint64(sys.Mtimespec.Sec),
			MtimeNs: uint32(sys.Mtimespec.Nsec),
			Nlink:   uint32(sys.Nlink),
			Rdev:    uint32(sys.Rdev),
		}
	default:
		return nil
	}
}

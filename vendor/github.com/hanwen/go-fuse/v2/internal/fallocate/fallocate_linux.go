package fallocate

import "golang.org/x/sys/unix"

func fallocate(fd int, mode uint32, off int64, len int64) error {
	return unix.Fallocate(fd, mode, off, len)
}

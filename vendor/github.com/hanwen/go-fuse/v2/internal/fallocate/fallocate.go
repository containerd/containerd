package fallocate

// Fallocate is a wrapper around fallocate syscall.
// On Linux, it is a wrapper around fallocate(2).
// On Darwin, it is a wrapper around fnctl(2).
// On FreeBSD, it is a wrapper around posix_fallocate(2).
func Fallocate(fd int, mode uint32, off int64, len int64) (err error) {
	return fallocate(fd, mode, off, len)
}

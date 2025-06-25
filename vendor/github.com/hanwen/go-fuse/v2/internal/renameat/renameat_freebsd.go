package renameat

import "golang.org/x/sys/unix"

const (
	// Since FreeBSD does not currently privode renameat syscall
	// beyond POSIX standard like Linux and Darwin do, we borrow
	// the defination from Linux but reject these non-POSIX flags.
	RENAME_EXCHANGE = (1 << 1)
)

func renameat(olddirfd int, oldpath string, newdirfd int, newpath string, flags uint) (err error) {
	if flags != 0 {
		return unix.ENOSYS
	}
	return unix.Renameat(olddirfd, oldpath, newdirfd, newpath)
}

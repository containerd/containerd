package renameat

import "golang.org/x/sys/unix"

const (
	RENAME_EXCHANGE = unix.RENAME_EXCHANGE
)

func renameat(olddirfd int, oldpath string, newdirfd int, newpath string, flags uint) (err error) {
	return unix.Renameat2(olddirfd, oldpath, newdirfd, newpath, flags)
}

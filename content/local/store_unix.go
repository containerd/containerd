// +build linux solaris darwin freebsd

package local

import (
	"os"
	"syscall"
	"time"

	"github.com/containerd/containerd/sys"
)

func getATime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return sys.StatATimeAsTime(st)
	}

	return fi.ModTime()
}

// +build darwin freebsd

package local

import (
	"os"
	"syscall"
	"time"
)

func getStartTime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec))
	}

	return fi.ModTime()
}

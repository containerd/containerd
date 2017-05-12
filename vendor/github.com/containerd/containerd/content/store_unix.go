// +build linux

package content

import (
	"os"
	"syscall"
	"time"
)

func getStartTime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}

	return fi.ModTime()
}

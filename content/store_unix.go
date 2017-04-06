// +build !windows

package content

import (
	"os"
	"syscall"
	"time"
)

func getStartedAt(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	}
	return fi.ModTime()
}

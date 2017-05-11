// +build !windows

package fsutils

import (
	"fmt"
	"github.com/containerd/containerd/mountinfo"
	"syscall"
)

// LookupMount returns the mount info corresponds to the path.
func LookupMount(dir string) (mountinfo.Info, error) {
	var dirStat syscall.Stat_t
	if err := syscall.Stat(dir, &dirStat); err != nil {
		return mountinfo.Info{}, fmt.Errorf("failed to access %q: %v", dir, err)
	}

	mounts, err := mountinfo.Self()
	if err != nil {
		return mountinfo.Info{}, err
	}
	for _, m := range mounts {
		// note that m.{Major, Minor} are generally unreliable; we don't use them here
		var st syscall.Stat_t
		if err := syscall.Stat(m.Mountpoint, &st); err != nil {
			// may fail; ignore err
			continue
		}
		if st.Dev == dirStat.Dev {
			return m, nil
		}
	}

	return mountinfo.Info{}, fmt.Errorf("failed to find the mount info for %q", dir)
}

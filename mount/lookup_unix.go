// +build !windows

package mount

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
)

// Lookup returns the mount info corresponds to the path.
func Lookup(dir string) (Info, error) {
	dir = filepath.Clean(dir)

	mounts, err := Self()
	if err != nil {
		return Info{}, err
	}

	// Sort descending order by Info.Mountpoint
	sort.SliceStable(mounts, func(i, j int) bool {
		return mounts[j].Mountpoint < mounts[i].Mountpoint
	})
	for _, m := range mounts {
		// Note that m.{Major, Minor} are generally unreliable for our purpose here
		// https://www.spinics.net/lists/linux-btrfs/msg58908.html
		// Note that device number is not checked here, because for overlayfs files
		// may have different device number with the mountpoint.
		if strings.HasPrefix(dir, m.Mountpoint) {
			return m, nil
		}
	}

	return Info{}, errors.Errorf("failed to find the mount info for %q", dir)
}

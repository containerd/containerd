// +build !windows

package containerd

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/mount"
	"golang.org/x/sys/unix"
)

func remapRootFS(mounts []mount.Mount, uid, gid uint32) error {
	root, err := ioutil.TempDir("", "ctd-remap")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for _, m := range mounts {
		if err := m.Mount(root); err != nil {
			return err
		}
	}
	defer unix.Unmount(root, 0)
	return filepath.Walk(root, incrementFS(root, uid, gid))
}

func incrementFS(root string, uidInc, gidInc uint32) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		var (
			stat = info.Sys().(*syscall.Stat_t)
			u, g = int(stat.Uid + uidInc), int(stat.Gid + gidInc)
		)
		// be sure the lchown the path as to not de-reference the symlink to a host file
		return os.Lchown(path, u, g)
	}
}

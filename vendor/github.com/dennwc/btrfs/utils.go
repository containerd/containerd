package btrfs

import (
	"fmt"
	"github.com/dennwc/btrfs/mtab"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func isBtrfs(path string) (bool, error) {
	var stfs syscall.Statfs_t
	if err := syscall.Statfs(path, &stfs); err != nil {
		return false, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	return int64(stfs.Type) == SuperMagic, nil
}

func findMountRoot(path string) (string, error) {
	mounts, err := mtab.Mounts()
	if err != nil {
		return "", err
	}
	longest := ""
	isBtrfs := false
	for _, m := range mounts {
		if !strings.HasPrefix(path, m.Mount) {
			continue
		}
		if len(longest) < len(m.Mount) {
			longest = m.Mount
			isBtrfs = m.Type == "btrfs"
		}
	}
	if longest == "" {
		return "", os.ErrNotExist
	} else if !isBtrfs {
		return "", ErrNotBtrfs{Path: longest}
	}
	return filepath.Abs(longest)
}

// openDir does the following checks before calling Open:
// 1: path is in a btrfs filesystem
// 2: path is a directory
func openDir(path string) (*os.File, error) {
	if ok, err := isBtrfs(path); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotBtrfs{Path: path}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	} else if st, err := file.Stat(); err != nil {
		file.Close()
		return nil, err
	} else if !st.IsDir() {
		file.Close()
		return nil, fmt.Errorf("not a directory: %s", path)
	}
	return file, nil
}

type searchResult struct {
	TransID  uint64
	ObjectID objectID
	Type     treeKeyType
	Offset   uint64
	Data     []byte
}

func treeSearchRaw(mnt *os.File, key btrfs_ioctl_search_key) (out []searchResult, _ error) {
	args := btrfs_ioctl_search_args{
		key: key,
	}
	if err := iocTreeSearch(mnt, &args); err != nil {
		return nil, err
	}
	out = make([]searchResult, 0, args.key.nr_items)
	buf := args.buf[:]
	for i := 0; i < int(args.key.nr_items); i++ {
		h := (*btrfs_ioctl_search_header)(unsafe.Pointer(&buf[0]))
		buf = buf[unsafe.Sizeof(btrfs_ioctl_search_header{}):]
		out = append(out, searchResult{
			TransID:  h.transid,
			ObjectID: h.objectid,
			Offset:   h.offset,
			Type:     h.typ,
			Data:     buf[:h.len:h.len], // TODO: reallocate?
		})
		buf = buf[h.len:]
	}
	return out, nil
}

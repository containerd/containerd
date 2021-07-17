package btrfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"
)

func Send(w io.Writer, parent string, subvols ...string) error {
	if len(subvols) == 0 {
		return nil
	}
	// use first send subvol to determine mount_root
	subvol, err := filepath.Abs(subvols[0])
	if err != nil {
		return err
	}
	mountRoot, err := findMountRoot(subvol)
	if err == os.ErrNotExist {
		return fmt.Errorf("cannot find a mountpoint for %s", subvol)
	} else if err != nil {
		return err
	}
	var (
		cloneSrc []objectID
		parentID objectID
	)
	if parent != "" {
		parent, err = filepath.Abs(parent)
		if err != nil {
			return err
		}
		id, err := getPathRootID(parent)
		if err != nil {
			return fmt.Errorf("cannot get parent root id: %v", err)
		}
		parentID = id
		cloneSrc = append(cloneSrc, id)
	}
	// check all subvolumes
	paths := make([]string, 0, len(subvols))
	for _, sub := range subvols {
		sub, err = filepath.Abs(sub)
		if err != nil {
			return err
		}
		paths = append(paths, sub)
		mount, err := findMountRoot(sub)
		if err != nil {
			return fmt.Errorf("cannot find mount root for %v: %v", sub, err)
		} else if mount != mountRoot {
			return fmt.Errorf("all subvolumes must be from the same filesystem (%s is not)", sub)
		}
		ok, err := IsReadOnly(sub)
		if err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("subvolume %s is not read-only", sub)
		}
	}
	mfs, err := Open(mountRoot, true)
	if err != nil {
		return err
	}
	defer mfs.Close()
	full := len(cloneSrc) == 0
	for i, sub := range paths {
		var rootID objectID
		if !full && parent != "" {
			rel, err := filepath.Rel(mountRoot, sub)
			if err != nil {
				return err
			}
			si, err := subvolSearchByPath(mfs.f, rel)
			if err != nil {
				return fmt.Errorf("cannot find subvolume %s: %v", rel, err)
			}
			rootID = objectID(si.RootID)
			parentID, err = findGoodParent(mfs.f, rootID, cloneSrc)
			if err != nil {
				return fmt.Errorf("cannot find good parent for %v: %v", rel, err)
			}
		}
		fs, err := Open(sub, true)
		if err != nil {
			return err
		}
		var flags uint64
		if i != 0 { // not first
			flags |= _BTRFS_SEND_FLAG_OMIT_STREAM_HEADER
		}
		if i < len(paths)-1 { // not last
			flags |= _BTRFS_SEND_FLAG_OMIT_END_CMD
		}
		err = send(w, fs.f, parentID, cloneSrc, flags)
		fs.Close()
		if err != nil {
			return fmt.Errorf("error sending %s: %v", sub, err)
		}
		if !full && parent != "" {
			cloneSrc = append(cloneSrc, rootID)
		}
	}
	return nil
}

func send(w io.Writer, subvol *os.File, parent objectID, sources []objectID, flags uint64) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() {
		defer pr.Close()
		_, err := io.Copy(w, pr)
		errc <- err
	}()
	fd := pw.Fd()
	wait := func() error {
		pw.Close()
		return <-errc
	}
	args := &btrfs_ioctl_send_args{
		send_fd:     int64(fd),
		parent_root: parent,
		flags:       flags,
	}
	if len(sources) != 0 {
		args.clone_sources = &sources[0]
		args.clone_sources_count = uint64(len(sources))
	}
	if err := iocSend(subvol, args); err != nil {
		wait()
		return err
	}
	return wait()
}

// readRootItem reads a root item from the tree.
//
// TODO(dennwc): support older kernels:
// In case we detect a root item smaller then sizeof(root_item),
// we know it's an old version of the root structure and initialize all new fields to zero.
// The same happens if we detect mismatching generation numbers as then we know the root was
// once mounted with an older kernel that was not aware of the root item structure change.
func readRootItem(mnt *os.File, rootID objectID) (*rootItem, error) {
	sk := btrfs_ioctl_search_key{
		tree_id: rootTreeObjectid,
		// There may be more than one ROOT_ITEM key if there are
		// snapshots pending deletion, we have to loop through them.
		min_objectid: rootID,
		max_objectid: rootID,
		min_type:     rootItemKey,
		max_type:     rootItemKey,
		max_offset:   maxUint64,
		max_transid:  maxUint64,
		nr_items:     4096,
	}
	for ; sk.min_offset < maxUint64; sk.min_offset++ {
		results, err := treeSearchRaw(mnt, sk)
		if err != nil {
			return nil, err
		} else if len(results) == 0 {
			break
		}
		for _, r := range results {
			sk.min_objectid = r.ObjectID
			sk.min_type = r.Type
			sk.min_offset = r.Offset
			if r.ObjectID > rootID {
				break
			}
			if r.ObjectID == rootID && r.Type == rootItemKey {
				const sz = int(unsafe.Sizeof(btrfs_root_item_raw{}))
				if len(r.Data) > sz {
					return nil, fmt.Errorf("btrfs_root_item is larger than expected; kernel is newer than the library")
				} else if len(r.Data) < sz { // TODO
					return nil, fmt.Errorf("btrfs_root_item is smaller then expected; kernel version is too old")
				}
				p := asRootItem(r.Data).Decode()
				return &p, nil
			}
		}
		results = nil
		if sk.min_type != rootItemKey || sk.min_objectid != rootID {
			break
		}
	}
	return nil, ErrNotFound
}

func getParent(mnt *os.File, rootID objectID) (*SubvolInfo, error) {
	st, err := subvolSearchByRootID(mnt, rootID, "")
	if err != nil {
		return nil, fmt.Errorf("cannot find subvolume %d to determine parent: %v", rootID, err)
	}
	return subvolSearchByUUID(mnt, st.ParentUUID)
}

func findGoodParent(mnt *os.File, rootID objectID, cloneSrc []objectID) (objectID, error) {
	parent, err := getParent(mnt, rootID)
	if err != nil {
		return 0, fmt.Errorf("get parent failed: %v", err)
	}
	for _, id := range cloneSrc {
		if id == objectID(parent.RootID) {
			return objectID(parent.RootID), nil
		}
	}
	var (
		bestParent *SubvolInfo
		bestDiff   uint64 = maxUint64
	)
	for _, id := range cloneSrc {
		parent2, err := getParent(mnt, id)
		if err == ErrNotFound {
			continue
		} else if err != nil {
			return 0, err
		}
		if parent2.RootID != parent.RootID {
			continue
		}
		parent2, err = subvolSearchByRootID(mnt, id, "")
		if err != nil {
			return 0, err
		}
		diff := int64(parent2.CTransID - parent.CTransID)
		if diff < 0 {
			diff = -diff
		}
		if uint64(diff) < bestDiff {
			bestParent, bestDiff = parent2, uint64(diff)
		}
	}
	if bestParent != nil {
		return objectID(bestParent.RootID), nil
	}
	if !parent.ParentUUID.IsZero() {
		return findGoodParent(mnt, objectID(parent.RootID), cloneSrc)
	}
	return 0, ErrNotFound
}

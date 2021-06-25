package btrfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func checkSubVolumeName(name string) bool {
	return name != "" && name[0] != 0 && !strings.ContainsRune(name, '/') &&
		name != "." && name != ".."
}

func IsSubVolume(path string) (bool, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return false, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if objectID(st.Ino) != firstFreeObjectid ||
		st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return false, nil
	}
	return isBtrfs(path)
}

func CreateSubVolume(path string) error {
	var inherit *btrfs_qgroup_inherit // TODO

	cpath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	newName := filepath.Base(cpath)
	dstDir := filepath.Dir(cpath)
	if !checkSubVolumeName(newName) {
		return fmt.Errorf("invalid subvolume name: %s", newName)
	} else if len(newName) >= volNameMax {
		return fmt.Errorf("subvolume name too long: %s", newName)
	}
	dst, err := openDir(dstDir)
	if err != nil {
		return err
	}
	defer dst.Close()
	if inherit != nil {
		panic("not implemented") // TODO
		args := btrfs_ioctl_vol_args_v2{
			flags: subvolQGroupInherit,
			btrfs_ioctl_vol_args_v2_u1: btrfs_ioctl_vol_args_v2_u1{
				//size: 	qgroup_inherit_size(inherit),
				qgroup_inherit: inherit,
			},
		}
		copy(args.name[:], newName)
		return iocSubvolCreateV2(dst, &args)
	}
	var args btrfs_ioctl_vol_args
	copy(args.name[:], newName)
	return iocSubvolCreate(dst, &args)
}

func DeleteSubVolume(path string) error {
	if ok, err := IsSubVolume(path); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("not a subvolume: %s", path)
	}
	cpath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dname := filepath.Dir(cpath)
	vname := filepath.Base(cpath)

	dir, err := openDir(dname)
	if err != nil {
		return err
	}
	defer dir.Close()
	var args btrfs_ioctl_vol_args
	copy(args.name[:], vname)
	return iocSnapDestroy(dir, &args)
}

func SnapshotSubVolume(subvol, dst string, ro bool) error {
	if ok, err := IsSubVolume(subvol); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("not a subvolume: %s", subvol)
	}
	exists := false
	if st, err := os.Stat(dst); err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil {
		if !st.IsDir() {
			return fmt.Errorf("'%s' exists and it is not a directory", dst)
		}
		exists = true
	}
	var (
		newName string
		dstDir  string
	)
	if exists {
		newName = filepath.Base(subvol)
		dstDir = dst
	} else {
		newName = filepath.Base(dst)
		dstDir = filepath.Dir(dst)
	}
	if !checkSubVolumeName(newName) {
		return fmt.Errorf("invalid snapshot name '%s'", newName)
	} else if len(newName) >= volNameMax {
		return fmt.Errorf("snapshot name too long '%s'", newName)
	}
	fdst, err := openDir(dstDir)
	if err != nil {
		return err
	}
	defer fdst.Close()
	// TODO: make SnapshotSubVolume a method on FS to use existing fd
	f, err := openDir(subvol)
	if err != nil {
		return fmt.Errorf("cannot open dest dir: %v", err)
	}
	defer f.Close()
	args := btrfs_ioctl_vol_args_v2{
		fd: int64(f.Fd()),
	}
	if ro {
		args.flags |= SubvolReadOnly
	}
	// TODO
	//if inherit != nil {
	//	args.flags |= subvolQGroupInherit
	//	args.size = qgroup_inherit_size(inherit)
	//	args.qgroup_inherit = inherit
	//}
	copy(args.name[:], newName)
	if err := iocSnapCreateV2(fdst, &args); err != nil {
		return fmt.Errorf("snapshot create failed: %v", err)
	}
	return nil
}

func IsReadOnly(path string) (bool, error) {
	f, err := GetFlags(path)
	if err != nil {
		return false, err
	}
	return f.ReadOnly(), nil
}

func GetFlags(path string) (SubvolFlags, error) {
	fs, err := Open(path, true)
	if err != nil {
		return 0, err
	}
	defer fs.Close()
	return fs.GetFlags()
}

func listSubVolumes(f *os.File, filter func(SubvolInfo) bool) (map[objectID]SubvolInfo, error) {
	sk := btrfs_ioctl_search_key{
		// search in the tree of tree roots
		tree_id: rootTreeObjectid,

		// Set the min and max to backref keys. The search will
		// only send back this type of key now.
		min_type: rootItemKey,
		max_type: rootBackrefKey,

		min_objectid: firstFreeObjectid,

		// Set all the other params to the max, we'll take any objectid
		// and any trans.
		max_objectid: lastFreeObjectid,
		max_offset:   maxUint64,
		max_transid:  maxUint64,

		nr_items: 4096, // just a big number, doesn't matter much
	}
	m := make(map[objectID]SubvolInfo)
	for {
		out, err := treeSearchRaw(f, sk)
		if err != nil {
			return nil, err
		} else if len(out) == 0 {
			break
		}
		for _, obj := range out {
			switch obj.Type {
			//case rootBackrefKey:
			//	ref := asRootRef(obj.Data)
			//	o := m[obj.ObjectID]
			//	o.TransID = obj.TransID
			//	o.ObjectID = obj.ObjectID
			//	o.RefTree = obj.Offset
			//	o.DirID = ref.DirID
			//	o.Name = ref.Name
			//	m[obj.ObjectID] = o
			case rootItemKey:
				o := m[obj.ObjectID]
				o.RootID = uint64(obj.ObjectID)
				robj := asRootItem(obj.Data).Decode()
				o.fillFromItem(&robj)
				m[obj.ObjectID] = o
			}
		}
		// record the mins in key so we can make sure the
		// next search doesn't repeat this root
		last := out[len(out)-1]
		sk.min_objectid = last.ObjectID
		sk.min_type = last.Type
		sk.min_offset = last.Offset + 1
		if sk.min_offset == 0 { // overflow
			sk.min_type++
		} else {
			continue
		}
		if sk.min_type > rootBackrefKey {
			sk.min_type = rootItemKey
			sk.min_objectid++
		} else {
			continue
		}
		if sk.min_objectid > sk.max_objectid {
			break
		}
	}
	// resolve paths
	for id, v := range m {
		if path, err := subvolidResolve(f, id); err == ErrNotFound {
			delete(m, id)
			continue
		} else if err != nil {
			return m, fmt.Errorf("cannot resolve path for %v: %v", id, err)
		} else {
			v.Path = path
			m[id] = v
		}
		if filter != nil && !filter(v) {
			delete(m, id)
		}
	}

	return m, nil
}

type SubvolInfo struct {
	RootID uint64

	UUID         UUID
	ParentUUID   UUID
	ReceivedUUID UUID

	CTime time.Time
	OTime time.Time
	STime time.Time
	RTime time.Time

	CTransID uint64
	OTransID uint64
	STransID uint64
	RTransID uint64

	Path string
}

func (s *SubvolInfo) fillFromItem(it *rootItem) {
	s.UUID = it.UUID
	s.ReceivedUUID = it.ReceivedUUID
	s.ParentUUID = it.ParentUUID

	s.CTime = it.CTime
	s.OTime = it.OTime
	s.STime = it.STime
	s.RTime = it.RTime

	s.CTransID = it.CTransID
	s.OTransID = it.OTransID
	s.STransID = it.STransID
	s.RTransID = it.RTransID
}

func subvolSearchByUUID(mnt *os.File, uuid UUID) (*SubvolInfo, error) {
	id, err := lookupUUIDSubvolItem(mnt, uuid)
	if err != nil {
		return nil, err
	}
	return subvolSearchByRootID(mnt, id, "")
}

func subvolSearchByReceivedUUID(mnt *os.File, uuid UUID) (*SubvolInfo, error) {
	id, err := lookupUUIDReceivedSubvolItem(mnt, uuid)
	if err != nil {
		return nil, err
	}
	return subvolSearchByRootID(mnt, id, "")
}

func subvolSearchByPath(mnt *os.File, path string) (*SubvolInfo, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(mnt.Name(), path)
	}
	id, err := getPathRootID(path)
	if err != nil {
		return nil, err
	}
	return subvolSearchByRootID(mnt, id, path)
}

func subvolidResolve(mnt *os.File, subvolID objectID) (string, error) {
	return subvolidResolveSub(mnt, "", subvolID)
}

func subvolidResolveSub(mnt *os.File, path string, subvolID objectID) (string, error) {
	if subvolID == fsTreeObjectid {
		return "", nil
	}
	sk := btrfs_ioctl_search_key{
		tree_id:      rootTreeObjectid,
		min_objectid: subvolID,
		max_objectid: subvolID,
		min_type:     rootBackrefKey,
		max_type:     rootBackrefKey,
		max_offset:   maxUint64,
		max_transid:  maxUint64,
		nr_items:     1,
	}
	results, err := treeSearchRaw(mnt, sk)
	if err != nil {
		return "", err
	} else if len(results) < 1 {
		return "", ErrNotFound
	}
	res := results[0]
	if objectID(res.Offset) != fsTreeObjectid {
		spath, err := subvolidResolveSub(mnt, path, objectID(res.Offset))
		if err != nil {
			return "", err
		}
		path = spath + "/"
	}
	backRef := asRootRef(res.Data)
	if backRef.DirID != firstFreeObjectid {
		arg := btrfs_ioctl_ino_lookup_args{
			treeid:   objectID(res.Offset),
			objectid: backRef.DirID,
		}
		if err := iocInoLookup(mnt, &arg); err != nil {
			return "", err
		}
		path += arg.Name()
	}
	return path + backRef.Name, nil
}

// subvolSearchByRootID
//
// Path is optional, and will be resolved automatically if not set.
func subvolSearchByRootID(mnt *os.File, rootID objectID, path string) (*SubvolInfo, error) {
	robj, err := readRootItem(mnt, rootID)
	if err != nil {
		return nil, err
	}
	info := &SubvolInfo{
		RootID: uint64(rootID),
		Path:   path,
	}
	info.fillFromItem(robj)
	if path == "" {
		info.Path, err = subvolidResolve(mnt, objectID(info.RootID))
	}
	return info, err
}

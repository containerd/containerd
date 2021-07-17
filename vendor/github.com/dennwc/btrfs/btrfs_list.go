package btrfs

import "os"

func getFileRootID(file *os.File) (objectID, error) {
	args := btrfs_ioctl_ino_lookup_args{
		objectid: firstFreeObjectid,
	}
	if err := iocInoLookup(file, &args); err != nil {
		return 0, err
	}
	return args.treeid, nil
}

func getPathRootID(path string) (objectID, error) {
	fs, err := Open(path, true)
	if err != nil {
		return 0, err
	}
	defer fs.Close()
	return getFileRootID(fs.f)
}

package mount

// Mount is the lingua franca of containerd. A mount represents a
// serialized mount syscall. Components either emit or consume mounts.
type Mount struct {
	// Type specifies the host-specific of the mount.
	Type string
	// Source specifies where to mount from. Depending on the host system, this
	// can be a source path or device.
	Source string
	// Options contains zero or more fstab-style mount options. Typically,
	// these are platform specific.
	Options []string
}

// MountAll mounts all the provided mounts to the provided target
func MountAll(mounts []Mount, target string) error {
	for _, m := range mounts {
		if err := m.Mount(target); err != nil {
			return err
		}
	}
	return nil
}

// UnmountN tries to unmount the given mount point nr times, which is
// useful for undoing a stack of mounts on the same mount
// point. Returns the first error encountered, but always attempts the
// full nr umounts.
func UnmountN(mount string, flags, nr int) error {
	var err error
	for i := 0; i < nr; i++ {
		if err2 := Unmount(mount, flags); err2 != nil {
			if err == nil {
				err = err2
			}
		}
	}
	return err
}

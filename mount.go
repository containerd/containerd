package containerd

import (
	"path/filepath"
	"strings"
	"syscall"
)

// Mount is the lingua franca of containerd. A mount represents a
// serialized mount syscall. Components either emit or consume mounts.
type Mount struct {
	// Type specifies the host-specific of the mount.
	Type string
	// Source specifies where to mount from. Depending on the host system, this
	// can be a source path or device.
	Source string
	// Target is a relative path to where a mount is expected to be
	// mounted given a mount root. When applied to a mount root, the mount
	// point becomes the concatenation of the provided mount root with
	// this relative target path. An empty target means the mount point
	// will be the mount root.
	Target string
	// Options contains zero or more fstab-style mount options. Typically,
	// these are platform specific.
	Options []string
}

// Mount mounts to the provided root. The mount point will use the mount's
// target as the relative path from the provided root.
func (m *Mount) Mount(root string) error {
	flags, data := parseMountOptions(m.Options)
	return syscall.Mount(m.Source, filepath.Join(root, m.Target), m.Type, uintptr(flags), data)
}

// Unmount unmounts at the provided root.
func (m *Mount) Unmount(root string) error {
	return syscall.Unmount(filepath.Join(root, m.Target), 0)
}

// MountAll mounts all the provided mounts to the provided root
func MountAll(mounts []Mount, root string) error {
	// TODO: ensure mounts are sorted from less specific
	// to more specific
	for _, m := range mounts {
		if err := m.Mount(root); err != nil {
			return err
		}
	}
	return nil
}

// UnmountAll unmounts all the provided mounts at the provided root
func UnmountAll(mounts []Mount, root string) error {
	// TODO: ensure mounts are sorted from more specific
	// to less specific
	for _, m := range mounts {
		if err := m.Unmount(root); err != nil {
			return err
		}
	}
	return nil
}

// parseMountOptions takes fstab style mount options and parses them for
// use with a standard mount() syscall
func parseMountOptions(options []string) (int, string) {
	var (
		flag int
		data []string
	)
	flags := map[string]struct {
		clear bool
		flag  int
	}{
		"async":         {true, syscall.MS_SYNCHRONOUS},
		"atime":         {true, syscall.MS_NOATIME},
		"bind":          {false, syscall.MS_BIND},
		"defaults":      {false, 0},
		"dev":           {true, syscall.MS_NODEV},
		"diratime":      {true, syscall.MS_NODIRATIME},
		"dirsync":       {false, syscall.MS_DIRSYNC},
		"exec":          {true, syscall.MS_NOEXEC},
		"mand":          {false, syscall.MS_MANDLOCK},
		"noatime":       {false, syscall.MS_NOATIME},
		"nodev":         {false, syscall.MS_NODEV},
		"nodiratime":    {false, syscall.MS_NODIRATIME},
		"noexec":        {false, syscall.MS_NOEXEC},
		"nomand":        {true, syscall.MS_MANDLOCK},
		"norelatime":    {true, syscall.MS_RELATIME},
		"nostrictatime": {true, syscall.MS_STRICTATIME},
		"nosuid":        {false, syscall.MS_NOSUID},
		"rbind":         {false, syscall.MS_BIND | syscall.MS_REC},
		"relatime":      {false, syscall.MS_RELATIME},
		"remount":       {false, syscall.MS_REMOUNT},
		"ro":            {false, syscall.MS_RDONLY},
		"rw":            {true, syscall.MS_RDONLY},
		"strictatime":   {false, syscall.MS_STRICTATIME},
		"suid":          {true, syscall.MS_NOSUID},
		"sync":          {false, syscall.MS_SYNCHRONOUS},
	}
	for _, o := range options {
		// If the option does not exist in the flags table or the flag
		// is not supported on the platform,
		// then it is a data value for a specific fs type
		if f, exists := flags[o]; exists && f.flag != 0 {
			if f.clear {
				flag &^= f.flag
			} else {
				flag |= f.flag
			}
		} else {
			data = append(data, o)
		}
	}
	return flag, strings.Join(data, ",")
}

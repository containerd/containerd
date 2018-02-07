package mount

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

func init() {
	t, err := TempLocation("/tmp")
	if err != nil {
		panic(err)
	}
	DefaultTempLocation = t
}

var DefaultTempLocation TempMounts

func TempLocation(root string) (TempMounts, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return DefaultTempLocation, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return DefaultTempLocation, err
	}
	return TempMounts{
		root: root,
	}, nil
}

type TempMounts struct {
	root string
}

// WithTempMount mounts the provided mounts to a temp dir, and pass the temp dir to f.
// The mounts are valid during the call to the f.
// Finally we will unmount and remove the temp dir regardless of the result of f.
func (t TempMounts) Mount(ctx context.Context, mounts []Mount, f func(root string) error) (err error) {
	root, uerr := ioutil.TempDir(t.root, "containerd-WithTempMount")
	if uerr != nil {
		return errors.Wrapf(uerr, "failed to create temp dir")
	}
	// We use Remove here instead of RemoveAll.
	// The RemoveAll will delete the temp dir and all children it contains.
	// When the Unmount fails, RemoveAll will incorrectly delete data from
	// the mounted dir. However, if we use Remove, even though we won't
	// successfully delete the temp dir and it may leak, we won't loss data
	// from the mounted dir.
	// For details, please refer to #1868 #1785.
	defer func() {
		if uerr = os.Remove(root); uerr != nil {
			log.G(ctx).WithError(uerr).WithField("dir", root).Errorf("failed to remove mount temp dir")
		}
	}()

	// We should do defer first, if not we will not do Unmount when only a part of Mounts are failed.
	defer func() {
		if uerr = UnmountAll(root, 0); uerr != nil {
			uerr = errors.Wrapf(uerr, "failed to unmount %s", root)
			if err == nil {
				err = uerr
			} else {
				err = errors.Wrap(err, uerr.Error())
			}
		}
	}()
	if uerr = All(mounts, root); uerr != nil {
		return errors.Wrapf(uerr, "failed to mount %s", root)
	}
	return errors.Wrapf(f(root), "mount callback failed on %s", root)
}

// Unmount all temp mounts and remove the directories
func (t TempMounts) Unmount(flags int) error {
	mounts, err := PID(os.Getpid())
	if err != nil {
		return err
	}
	var toUnmount []string
	for _, m := range mounts {
		if strings.HasPrefix(m.Mountpoint, t.root) {
			toUnmount = append(toUnmount, m.Mountpoint)
		}
	}
	sort.Sort(sort.Reverse(mountSorter(toUnmount)))
	for _, path := range toUnmount {
		if err := UnmountAll(path, flags); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

type mountSorter []string

func (by mountSorter) Len() int {
	return len(by)
}

func (by mountSorter) Less(i, j int) bool {
	is := strings.Split(by[i], string(os.PathSeparator))
	js := strings.Split(by[j], string(os.PathSeparator))
	return len(is) < len(js)
}

func (by mountSorter) Swap(i, j int) {
	by[i], by[j] = by[j], by[i]
}

package mount

import (
	"io/ioutil"
	"os"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

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

// All mounts all the provided mounts to the provided target
func All(mounts []Mount, target string) error {
	for _, m := range mounts {
		if err := m.Mount(target); err != nil {
			return err
		}
	}
	return nil
}

// WithTempMount mounts the provided mounts to a temp dir, and pass the temp dir to f.
// The mounts are valid during the call to the f.
// Finally we will unmount and remove the temp dir regardless of the result of f.
func WithTempMount(mounts []Mount, f func(root string) error) (err error) {
	root, uerr := ioutil.TempDir("", "containerd-WithTempMount")
	if uerr != nil {
		return errors.Wrapf(uerr, "failed to create temp dir for %v", mounts)
	}
	// We use Remove here instead of RemoveAll.
	// The RemoveAll will delete the temp dir and all children it contains.
	// When the Unmount fails, if we use RemoveAll, We will incorrectly delete data from mounted dir.
	// if we use Remove,even though we won't successfully delete the temp dir,
	// but we only leak a temp dir, we don't loss data from mounted dir.
	// For details, please refer to #1868 #1785.
	defer func() {
		if uerr = os.Remove(root); uerr != nil {
			log.L.Errorf("Failed to remove the temp dir %s: %v", root, uerr)
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

	if uerr = f(root); uerr != nil {
		return errors.Wrapf(uerr, "failed to f(%s)", root)
	}
	return nil
}

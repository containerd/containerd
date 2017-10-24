// +build darwin freebsd

package mount

import "github.com/pkg/errors"

var (
	ErrNotImplementOnUnix = errors.New("not implemented under unix")
)

func (m *Mount) Mount(target string) error {
	return ErrNotImplementOnUnix
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

func Unmount(mount string, flags int) error {
	return ErrNotImplementOnUnix
}

func UnmountAll(mount string, flags int) error {
	return ErrNotImplementOnUnix
}

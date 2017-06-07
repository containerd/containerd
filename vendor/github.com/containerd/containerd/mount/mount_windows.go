package mount

import "github.com/pkg/errors"

var (
	ErrNotImplementOnWindows = errors.New("not implemented under windows")
)

func (m *Mount) Mount(target string) error {
	return ErrNotImplementOnWindows
}

func Unmount(mount string, flags int) error {
	return ErrNotImplementOnWindows
}

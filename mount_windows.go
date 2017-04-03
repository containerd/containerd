package containerd

import (
	"errors"
)

func (m *Mount) Mount(target string) error {
	return errors.New("mount not supported")
}

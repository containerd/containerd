package labels

import (
	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"
)

const (
	maxSize = 4096
)

func Validate(k, v string) error {
	// A label key and value should be under 4096 bytes
	if (len(k) + len(v)) > maxSize {
		if len(k) > 10 {
			k = k[:10]
		}

		return errors.Wrapf(errdefs.ErrInvalidArgument, "label key and value greater than maximum size (%d bytes), key: %s", maxSize, k)
	}

	return nil
}

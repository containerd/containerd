package fstest

import (
	"time"

	"github.com/pkg/errors"
)

// Lchtimes changes access and mod time of file without following symlink
func Lchtimes(name string, atime, mtime time.Time) Applier {
	return applyFn(func(root string) error {
		return errors.New("Not implemented")
	})
}

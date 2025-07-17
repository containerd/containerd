//go:build linux

package fstest

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Applier is an interface for applying filesystem changes.
type Applier interface {
	Apply(root string) error
}

type applyFn func(root string) error

func (fn applyFn) Apply(root string) error {
	return fn(root)
}

// ChownFunc is used by Chown() to change ownership.
// Default: os.Chown.
// Tests can overwrite this variable to inject deterministic errors.
var ChownFunc = os.Chown

// Chown changes uid/gid of the given path and retries on transient
// failures such as EBUSY / EPERM.
//
// Retry policy: 5 attempts, linear 10 ms back-off.
func Chown(path string, uid, gid int) Applier {
	return applyFn(func(root string) error {
		abs := filepath.Join(root, path)
		return Retry(context.Background(), 5, 10*time.Millisecond, func() error {
			return ChownFunc(abs, uid, gid)
		})
	})
}

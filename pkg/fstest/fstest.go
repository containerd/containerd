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

// FileOp represents a file operation.
type FileOp interface {
	Apply(root string) error
}

// ChownFunc is used by Chown() to change ownership.
var ChownFunc = os.Chown

// Chown changes uid/gid of the given path and retries on transient failures.
func Chown(path string, uid, gid int) Applier {
	return applyFn(func(root string) error {
		abs := filepath.Join(root, path)
		return Retry(context.Background(), 5, 10*time.Millisecond, func() error {
			return ChownFunc(abs, uid, gid)
		})
	})
}

// Apply applies a series of file operations to the given root directory.
func Apply(root string, ops ...FileOp) error {
	for _, op := range ops {
		if err := op.Apply(root); err != nil {
			return err
		}
	}
	return nil
}

// CreateFile creates a file with the given content and permissions.
func CreateFile(path string, data []byte, perm os.FileMode) FileOp {
	return applyFn(func(root string) error {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(fullPath, data, perm)
	})
}

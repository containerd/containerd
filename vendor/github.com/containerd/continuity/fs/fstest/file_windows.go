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

// Base applies the files required to make a valid Windows container layer
// that the filter will mount. It is used for testing the snapshotter
func Base() Applier {
	return Apply(
		CreateDir("Windows", 0755),
		CreateDir("Windows/System32", 0755),
		CreateDir("Windows/System32/Config", 0755),
		CreateFile("Windows/System32/Config/SYSTEM", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SOFTWARE", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SAM", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SECURITY", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/DEFAULT", []byte("foo\n"), 0777),
	)
}

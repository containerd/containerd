//+build !windows

package log

import (
	"path/filepath"
)

func normalizePath(p string) (string, error) {
	return filepath.Abs(p)
}

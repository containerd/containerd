//go:build !windows
// +build !windows

package files

import (
	"os"
)

func isHidden(fi os.FileInfo) bool {
	fName := fi.Name()
	switch fName {
	case "", ".", "..":
		return false
	default:
		return fName[0] == '.'
	}
}

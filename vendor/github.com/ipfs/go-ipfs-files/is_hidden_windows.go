//go:build windows
// +build windows

package files

import (
	"os"

	windows "golang.org/x/sys/windows"
)

func isHidden(fi os.FileInfo) bool {
	fName := fi.Name()
	switch fName {
	case "", ".", "..":
		return false
	}

	if fName[0] == '.' {
		return true
	}

	sys := fi.Sys()
	if sys == nil {
		return false
	}
	wi, ok := sys.(*windows.Win32FileAttributeData)
	if !ok {
		return false
	}

	return wi.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
}

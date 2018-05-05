package sysx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// isRoot returns true if path is root of file system
// (`/` on unix and `/`, `\`, `c:\` or `c:/` on windows).
func isRoot(path string) bool {
	if runtime.GOOS != "windows" {
		return path == "/"
	}
	fmt.Printf("IS ROOT? %s\n", path)
	switch len(path) {
	case 1:
		return os.IsPathSeparator(path[0])
	case 3:
		return path[1] == ':' && os.IsPathSeparator(path[2])
	// \??\Volume{a309d415-2718-43a1-8fca-020b03a69d48}\
	case 49:
		return strings.HasPrefix(path, `\??\Volume{`)
	}
	return false
}

// isDriveLetter returns true if path is Windows drive letter (like "c:").
func isDriveLetter(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return len(path) == 2 && path[1] == ':'
}

func walkLink(path string, linksWalked *int) (newpath string, islink bool, err error) {
	if *linksWalked > 255 {
		return "", false, errors.New("EvalSymlinks: too many links")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, false, nil
	}
	newpath, err = Readlink(path)
	if err != nil {
		return "", false, err
	}
	fmt.Printf("ReadLink returned: %s\n", newpath)
	*linksWalked++
	return newpath, true, nil
}

func walkLinks(path string, linksWalked *int) (string, error) {
	switch dir, file := Split(path); {
	case dir == "":
		newpath, _, err := walkLink(file, linksWalked)
		return newpath, err
	case file == "":
		if isDriveLetter(dir) {
			return dir, nil
		}
		if os.IsPathSeparator(dir[len(dir)-1]) {
			if isRoot(dir) {
				return dir, nil
			}
			return walkLinks(dir[:len(dir)-1], linksWalked)
		}
		newpath, _, err := walkLink(dir, linksWalked)
		return newpath, err
	default:
		newdir, err := walkLinks(dir, linksWalked)
		if err != nil {
			return "", err
		}
		newpath, islink, err := walkLink(filepath.Join(newdir, file), linksWalked)
		if err != nil {
			return "", err
		}
		if !islink {
			return newpath, nil
		}
		if filepath.IsAbs(newpath) || os.IsPathSeparator(newpath[0]) {
			return newpath, nil
		}
		return filepath.Join(newdir, newpath), nil
	}
}

func walkSymlinks(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	var linksWalked int // to protect against cycles
	for {
		i := linksWalked
		newpath, err := walkLinks(path, &linksWalked)
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "windows" {
			// walkLinks(".", ...) always returns "." on unix.
			// But on windows it returns symlink target, if current
			// directory is a symlink. Stop the walk, if symlink
			// target is not absolute path, and return "."
			// to the caller (just like unix does).
			// Same for "C:.".
			if path[volumeNameLen(path):] == "." && !filepath.IsAbs(newpath) {
				return path, nil
			}
		}
		if i == linksWalked {
			return filepath.Clean(newpath), nil
		}
		path = newpath
	}
}

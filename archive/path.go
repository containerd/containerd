package archive

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

var (
	errTooManyLinks = errors.New("too many links")
)

// rootPath joins a path with a root, evaluating and bounding any
// symlink to the root directory.
// TODO(dmcgowan): Expose and move to fs package or continuity path driver
func rootPath(root, path string) (string, error) {
	if path == "" {
		return root, nil
	}
	var linksWalked int // to protect against cycles
	for {
		i := linksWalked
		newpath, err := walkLinks(root, path, &linksWalked)
		if err != nil {
			return "", err
		}
		path = newpath
		if i == linksWalked {
			newpath = filepath.Join("/", newpath)
			if path == newpath {
				return filepath.Join(root, newpath), nil
			}
			path = newpath
		}
	}
}

func walkLink(root, path string, linksWalked *int) (newpath string, islink bool, err error) {
	if *linksWalked > 255 {
		return "", false, errTooManyLinks
	}

	path = filepath.Join("/", path)
	if path == "/" {
		return path, false, nil
	}
	realPath := filepath.Join(root, path)

	fi, err := os.Lstat(realPath)
	if err != nil {
		// If path does not yet exist, treat as non-symlink
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, false, nil
	}
	newpath, err = os.Readlink(realPath)
	if err != nil {
		return "", false, err
	}
	if filepath.IsAbs(newpath) && strings.HasPrefix(newpath, root) {
		newpath = newpath[:len(root)]
		if !strings.HasPrefix(newpath, "/") {
			newpath = "/" + newpath
		}
	}
	*linksWalked++
	return newpath, true, nil
}

func walkLinks(root, path string, linksWalked *int) (string, error) {
	switch dir, file := filepath.Split(path); {
	case dir == "":
		newpath, _, err := walkLink(root, file, linksWalked)
		return newpath, err
	case file == "":
		if os.IsPathSeparator(dir[len(dir)-1]) {
			if dir == "/" {
				return dir, nil
			}
			return walkLinks(root, dir[:len(dir)-1], linksWalked)
		}
		newpath, _, err := walkLink(root, dir, linksWalked)
		return newpath, err
	default:
		newdir, err := walkLinks(root, dir, linksWalked)
		if err != nil {
			return "", err
		}
		newpath, islink, err := walkLink(root, filepath.Join(newdir, file), linksWalked)
		if err != nil {
			return "", err
		}
		if !islink {
			return newpath, nil
		}
		if filepath.IsAbs(newpath) {
			return newpath, nil
		}
		return filepath.Join(newdir, newpath), nil
	}
}

package fs

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

// CopyDirectory copies the directory from src to dst.
// Most efficient copy of files is attempted.
func CopyDirectory(dst, src string) error {
	inodes := map[uint64]string{}
	return copyDirectory(dst, src, inodes)
}

func copyDirectory(dst, src string, inodes map[uint64]string) error {
	stat, err := os.Stat(src)
	if err != nil {
		return errors.Wrapf(err, "failed to stat %s", src)
	}
	if !stat.IsDir() {
		return errors.Errorf("source is not directory")
	}

	if _, err := os.Stat(dst); err != nil {
		if err := os.Mkdir(dst, stat.Mode()); err != nil {
			return errors.Wrapf(err, "failed to mkdir %s", dst)
		}
	} else {
		if err := os.Chmod(dst, stat.Mode()); err != nil {
			return errors.Wrapf(err, "failed to chmod on %s", dst)
		}
	}

	fis, err := ioutil.ReadDir(src)
	if err != nil {
		return errors.Wrapf(err, "failed to read %s", src)
	}

	if err := copyFileInfo(stat, dst); err != nil {
		return errors.Wrapf(err, "failed to copy file info for %s", dst)
	}

	for _, fi := range fis {
		source := filepath.Join(src, fi.Name())
		target := filepath.Join(dst, fi.Name())
		if fi.IsDir() {
			if err := copyDirectory(target, source, inodes); err != nil {
				return err
			}
			continue
		} else if (fi.Mode() & os.ModeType) == 0 {
			link, err := GetLinkSource(target, fi, inodes)
			if err != nil {
				return errors.Wrap(err, "failed to get hardlink")
			}
			if link != "" {
				if err := os.Link(link, target); err != nil {
					return errors.Wrap(err, "failed to create hard link")
				}
			} else if err := copyFile(source, target); err != nil {
				return errors.Wrap(err, "failed to copy files")
			}
		} else if (fi.Mode() & os.ModeSymlink) == os.ModeSymlink {
			link, err := os.Readlink(source)
			if err != nil {
				return errors.Wrapf(err, "failed to read link: %s", source)
			}
			if err := os.Symlink(link, target); err != nil {
				return errors.Wrapf(err, "failed to create symlink: %s", target)
			}
		} else if (fi.Mode() & os.ModeDevice) == os.ModeDevice {
			// TODO: support devices
			return errors.New("devices not supported")
		} else {
			// TODO: Support pipes and sockets
			return errors.Wrapf(err, "unsupported mode %s", fi.Mode())
		}
		if err := copyFileInfo(fi, target); err != nil {
			return errors.Wrap(err, "failed to copy file info")
		}

		if err := copyXAttrs(target, source); err != nil {
			return errors.Wrap(err, "failed to copy xattrs")
		}
	}

	return nil
}

func copyFile(source, target string) error {
	src, err := os.Open(source)
	if err != nil {
		return errors.Wrapf(err, "failed to open source %s", err)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return errors.Wrapf(err, "failed to open target %s", err)
	}
	defer tgt.Close()

	return copyFileContent(tgt, src)
}

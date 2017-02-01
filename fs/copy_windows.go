package fs

import (
	"io"
	"os"

	"github.com/pkg/errors"
)

func copyFileInfo(fi os.FileInfo, name string) error {
	if err := os.Chmod(name, fi.Mode()); err != nil {
		return errors.Wrapf(err, "failed to chmod %s", name)
	}

	// TODO: copy windows specific metadata

	return nil
}

func copyFileContent(dst, src *os.File) error {
	_, err := io.Copy(dst, src)
	return err
}

func copyXAttrs(dst, src string) error {
	return nil
}

func copyDevice(dst string, fi os.FileInfo) error {
	return errors.New("device copy not supported")
}

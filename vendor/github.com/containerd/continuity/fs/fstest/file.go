/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package fstest

import (
	"bytes"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Applier applies single file changes
type Applier interface {
	Apply(root string) error
}

type applyFn func(root string) error

func (a applyFn) Apply(root string) error {
	return a(root)
}

// CreateFile returns a file applier which creates a file as the
// provided name with the given content and permission.
func CreateFile(name string, content []byte, perm os.FileMode) Applier {
	f := func() io.Reader {
		return bytes.NewReader(content)
	}
	return writeFileStream(name, f, perm)
}

// CreateRandomFile returns a file applier which creates a file with random
// content of the given size using the given seed and permission.
func CreateRandomFile(name string, seed, size int64, perm os.FileMode) Applier {
	f := func() io.Reader {
		return io.LimitReader(rand.New(rand.NewSource(seed)), size)
	}
	return writeFileStream(name, f, perm)
}

// writeFileStream returns a file applier which creates a file as the
// provided name with the given content from the provided i/o stream and permission.
func writeFileStream(name string, stream func() io.Reader, perm os.FileMode) Applier {
	return applyFn(func(root string) (retErr error) {
		fullPath := filepath.Join(root, name)
		f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
		if err != nil {
			return err
		}
		defer func() {
			err := f.Close()
			if err != nil && retErr == nil {
				retErr = err
			}
		}()
		_, err = io.Copy(f, stream())
		if err != nil {
			return err
		}
		return os.Chmod(fullPath, perm)
	})
}

// Remove returns a file applier which removes the provided file name
func Remove(name string) Applier {
	return applyFn(func(root string) error {
		return os.Remove(filepath.Join(root, name))
	})
}

// RemoveAll returns a file applier which removes the provided file name
// as in os.RemoveAll
func RemoveAll(name string) Applier {
	return applyFn(func(root string) error {
		return os.RemoveAll(filepath.Join(root, name))
	})
}

// CreateDir returns a file applier to create the directory with
// the provided name and permission
func CreateDir(name string, perm os.FileMode) Applier {
	return applyFn(func(root string) error {
		fullPath := filepath.Join(root, name)
		if err := os.MkdirAll(fullPath, perm); err != nil {
			return err
		}
		return os.Chmod(fullPath, perm)
	})
}

// Rename returns a file applier which renames a file
func Rename(old, new string) Applier {
	return applyFn(func(root string) error {
		return os.Rename(filepath.Join(root, old), filepath.Join(root, new))
	})
}

// Chown returns a file applier which changes the ownership of a file
func Chown(name string, uid, gid int) Applier {
	return applyFn(func(root string) error {
		return os.Chown(filepath.Join(root, name), uid, gid)
	})
}

// Chtimes changes access and mod time of file.
// Use Lchtimes for symbolic links.
func Chtimes(name string, atime, mtime time.Time) Applier {
	return applyFn(func(root string) error {
		return os.Chtimes(filepath.Join(root, name), atime, mtime)
	})
}

// Chmod returns a file applier which changes the file permission
func Chmod(name string, perm os.FileMode) Applier {
	return applyFn(func(root string) error {
		return os.Chmod(filepath.Join(root, name), perm)
	})
}

// Symlink returns a file applier which creates a symbolic link
func Symlink(oldname, newname string) Applier {
	return applyFn(func(root string) error {
		return os.Symlink(oldname, filepath.Join(root, newname))
	})
}

// Link returns a file applier which creates a hard link
func Link(oldname, newname string) Applier {
	return applyFn(func(root string) error {
		return os.Link(filepath.Join(root, oldname), filepath.Join(root, newname))
	})
}

// TODO: Make platform specific, windows applier is always no-op
//func Mknod(name string, mode int32, dev int) Applier {
//	return func(root string) error {
//		return return syscall.Mknod(path, mode, dev)
//	}
//}

func CreateSocket(name string, perm os.FileMode) Applier {
	return applyFn(func(root string) error {
		fullPath := filepath.Join(root, name)
		ln, err := net.Listen("unix", fullPath)
		if err != nil {
			return err
		}
		defer ln.Close()
		return os.Chmod(fullPath, perm)
	})
}

// Apply returns a new applier from the given appliers
func Apply(appliers ...Applier) Applier {
	return applyFn(func(root string) error {
		for _, a := range appliers {
			if err := a.Apply(root); err != nil {
				return err
			}
		}
		return nil
	})
}

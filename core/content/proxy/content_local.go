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

package proxy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	digest "github.com/opencontainers/go-digest"
)

// localBlobPath returns the path of a blob under the blobs directory, using the
// layout of containerd's local content store: <blobs>/<algorithm>/<encoded>.
func localBlobPath(blobs string, dgst digest.Digest) (string, error) {
	rel, err := blobRel(dgst)
	if err != nil {
		return "", err
	}
	return filepath.Join(blobs, rel), nil
}

// blobRel returns the path of a blob relative to the blobs directory.
func blobRel(dgst digest.Digest) (string, error) {
	// Validation ensures the algorithm is known and the encoded part only
	// contains characters valid for it.
	if err := dgst.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(dgst.Algorithm().String(), dgst.Encoded()), nil
}

// openLocal opens a blob under the blobs directory for reading. The blob must be
// a regular file and, when size is non-zero, of that size, otherwise it is not
// used.
func openLocal(blobs string, dgst digest.Digest, size int64) (*localReaderAt, error) {
	rel, err := blobRel(dgst)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(blobs, rel)
	fp, err := openRegular(blobs, rel)
	if err != nil {
		return nil, err
	}
	fi, err := fp.Stat()
	if err != nil {
		fp.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		fp.Close()
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	if size != 0 && fi.Size() != size {
		fp.Close()
		return nil, fmt.Errorf("%s has size %d, expected %d", p, fi.Size(), size)
	}
	return &localReaderAt{fp: fp, size: fi.Size()}, nil
}

// openRegular opens rel under the blobs directory for reading, without
// following symlinks out of the directory or blocking:
//   - A FIFO opened for read blocks until a writer appears, so it is opened
//     non-blocking, leaving openLocal to reject it. O_NONBLOCK has no effect on
//     a regular file, and none on Windows, where the open cannot block.
//   - os.OpenRoot opens the directory with a plain open, which would block on a
//     FIFO at that path, so on Unix a trailing slash makes the open fail unless
//     the path is a directory.
//
// On Windows the file is shared for deletion, so the remote store can delete
// or rename a blob while containerd reads it.
func openRegular(blobs, rel string) (*os.File, error) {
	if runtime.GOOS != "windows" && !strings.HasSuffix(blobs, "/") {
		blobs += "/"
	}
	root, err := os.OpenRoot(blobs)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// localReaderAt reads a blob from a local file.
type localReaderAt struct {
	fp   *os.File
	size int64
}

func (ra *localReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return ra.fp.ReadAt(p, off)
}

func (ra *localReaderAt) Size() int64 {
	return ra.size
}

func (ra *localReaderAt) Close() error {
	return ra.fp.Close()
}

// Reader returns a sequential reader over the file. A limited *os.File lets
// io.Copy to another file use copy_file_range where it is supported.
func (ra *localReaderAt) Reader() io.Reader {
	return io.LimitReader(ra.fp, ra.size)
}

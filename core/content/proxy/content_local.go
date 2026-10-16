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

	digest "github.com/opencontainers/go-digest"
)

// localBlobPath returns the path of a blob under the blobs directory, using the
// layout of containerd's local content store: <blobs>/<algorithm>/<encoded>.
func localBlobPath(blobs string, dgst digest.Digest) (string, error) {
	// Validation ensures the algorithm is known and the encoded part only
	// contains characters valid for it, so the path cannot leave the directory.
	if err := dgst.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(blobs, dgst.Algorithm().String(), dgst.Encoded()), nil
}

// openLocal opens a blob under the blobs directory for reading. The blob must be
// a regular file of the expected size, otherwise it is not used.
func openLocal(blobs string, dgst digest.Digest, size int64) (*localReaderAt, error) {
	p, err := localBlobPath(blobs, dgst)
	if err != nil {
		return nil, err
	}
	fp, err := openRegular(p)
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
	if fi.Size() != size {
		fp.Close()
		return nil, fmt.Errorf("%s has size %d, expected %d", p, fi.Size(), size)
	}
	return &localReaderAt{fp: fp, size: size}, nil
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

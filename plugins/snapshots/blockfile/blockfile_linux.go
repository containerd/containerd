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

package blockfile

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const (
	SeekData = 3
	SeekHole = 4
)

func copyFileWithSync(target, source string, copySparse bool) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", source, err)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to open target %s: %w", target, err)
	}
	defer tgt.Close()
	defer tgt.Sync()

	if copySparse {
		info, err := src.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat the source %s: %w", source, err)
		}

		srcSize := info.Size()
		if err = tgt.Truncate(srcSize); err != nil {
			return fmt.Errorf("failed to truncate the target %s: %w", target, err)
		}

		srcFd := int(src.Fd())
		tgtFd := int(tgt.Fd())
		srcOffset := int64(0)
		tgtOffset := int64(0)

		n := int(srcSize)
		for n > 0 {
			copied, err := unix.CopyFileRange(srcFd, &srcOffset, tgtFd, &tgtOffset, n, 0)
			if err != nil || copied == 0 {
				return fmt.Errorf("failed to copy file to target %s: %w", target, err)
			}
			n -= copied
		}
		return nil
	}

	_, err = io.Copy(tgt, src)
	if err != nil {
		return fmt.Errorf("failed to copy file to target %s: %w", target, err)
	}

	return err
}

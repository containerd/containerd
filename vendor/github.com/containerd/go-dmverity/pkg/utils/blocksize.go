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

package utils

import "fmt"

// CalculateDataBlocks derives the number of data blocks for a device or file.
// If userSpecified is non-zero it is returned directly; otherwise the device
// size is queried via GetBlockOrFileSize and divided by dataBlockSize.
//
// GetBlockOrFileSize uses the BLKGETSIZE64 ioctl on Linux to support block
// devices. On other platforms only regular files are supported.
func CalculateDataBlocks(dataPath string, userSpecified uint64, dataBlockSize uint32) (uint64, error) {
	if userSpecified != 0 {
		return userSpecified, nil
	}

	size, err := GetBlockOrFileSize(dataPath)
	if err != nil {
		return 0, fmt.Errorf("determine data device size: %w", err)
	}

	if size <= 0 {
		return 0, fmt.Errorf("cannot determine data size; provide --data-blocks")
	}

	if dataBlockSize == 0 {
		return 0, fmt.Errorf("data block size required")
	}

	if size%int64(dataBlockSize) != 0 {
		return 0, fmt.Errorf("data size %d not multiple of data block size %d",
			size, dataBlockSize)
	}

	return uint64(size / int64(dataBlockSize)), nil
}

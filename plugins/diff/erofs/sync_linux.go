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

package erofs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// syncDir flushes all pending writes on the filesystem containing dir to
// stable storage, mirroring what the walking differ does via its SyncFs
// apply option. Without it a power loss can leave the snapshot committed
// in the metadata store while its layer.erofs blob is truncated.
func syncDir(dir string) error {
	fd, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", dir, err)
	}
	defer fd.Close()

	if err := unix.Syncfs(int(fd.Fd())); err != nil {
		return fmt.Errorf("failed to syncfs for %s: %w", dir, err)
	}
	return nil
}

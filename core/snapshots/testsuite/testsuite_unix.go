//go:build !windows

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

package testsuite

import (
	"syscall"
	"testing"
)

func clearMask() func() {
	oldumask := syscall.Umask(0)
	return func() {
		syscall.Umask(oldumask)
	}
}

func debugDiskUsage(t *testing.T, root string) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err == nil {
		t.Logf("Disk space for %s: blocks: %d, bfree: %d, bavail: %d, bsize: %d, free: %d MB, avail: %d MB",
			root, stat.Blocks, stat.Bfree, stat.Bavail, stat.Bsize,
			(uint64(stat.Bfree)*uint64(stat.Bsize))/(1024*1024),
			(uint64(stat.Bavail)*uint64(stat.Bsize))/(1024*1024),
		)
	}
}

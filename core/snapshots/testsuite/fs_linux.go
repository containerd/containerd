//go:build linux

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

import "golang.org/x/sys/unix"

// mknodChar creates an overlay whiteout: a character device with device
// number 0. This requires CAP_MKNOD (root).
func mknodChar(path string) error {
	return unix.Mknod(path, unix.S_IFCHR|0o000, 0)
}

// setOpaque marks a directory opaque so overlay hides lower-layer content
// beneath it. This requires the privilege to set trusted xattrs (root).
func setOpaque(path string) error {
	return unix.Setxattr(path, "trusted.overlay.opaque", []byte("y"), 0)
}

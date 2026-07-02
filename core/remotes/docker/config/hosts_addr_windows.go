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

package config

import (
	"path/filepath"
)

// nativeUnixAddr returns the kernel-level address for net.Dial("unix", ...)
// from a dial_addr URL path. dial_addr is a URL (e.g. "unix:///C:/foo.sock"),
// so paths reach us with forward slashes and a leading "/" before the drive
// letter, matching the file:// URI convention. Go's net package on Windows
// expects native paths (e.g. "C:\foo.sock"), so for any path whose first
// character is "/" followed by a drive letter we strip the leading "/" and
// convert separators to backslashes. Abstract addresses ("@name") and
// drive-less paths pass through unchanged.
func nativeUnixAddr(addr string) string {
	if len(addr) >= 3 && addr[0] == '/' && isDriveLetter(addr[1]) && addr[2] == ':' {
		addr = addr[1:]
	}
	return filepath.FromSlash(addr)
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

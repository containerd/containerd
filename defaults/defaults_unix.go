// +build !windows

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

package defaults

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultRootDir is the default location used by containerd to store
	// persistent data
	DefaultRootDir = "/var/lib/containerd"
	// DefaultStateDir is the default location used by containerd to store
	// transient data
	DefaultStateDir = "/run/containerd"
	// DefaultAddress is the default unix socket address
	DefaultAddress = "/run/containerd/containerd.sock"
	// DefaultDebugAddress is the default unix socket address for pprof data
	DefaultDebugAddress = "/run/containerd/debug.sock"
	// DefaultFIFODir is the default location used by client-side cio library
	// to store FIFOs.
	DefaultFIFODir = "/run/containerd/fifo"
)

func init() {
	//  pam_systemd sets XDG_RUNTIME_DIR but not other dirs.
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		dirs := strings.Split(xdgDataHome, ":")
		UserRootDir = filepath.Join(dirs[0], "containerd")
	} else if home := os.Getenv("HOME"); home != "" {
		UserRootDir = filepath.Join(home, ".local", "share", "containerd")
	}
	if xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntimeDir != "" {
		dirs := strings.Split(xdgRuntimeDir, ":")
		UserStateDir = filepath.Join(dirs[0], "containerd")
		UserAddress = filepath.Join(UserStateDir, "containerd.sock")
		UserDebugAddress = filepath.Join(UserStateDir, "debug.sock")
		UserFIFODir = filepath.Join(UserStateDir, "fifo")
	}
}

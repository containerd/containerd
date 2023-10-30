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

package runc

import (
	"net"
	"os"
	"path/filepath"
)

// NewTempPidfdSocket returns a temp pidfd socket to accept a pidfd created by
// runc. The socket will be deleted on Close().
func NewTempPidfdSocket() (*Socket, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	dir, err := os.MkdirTemp(runtimeDir, "pidfd")
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(filepath.Join(dir, "pidfd.sock"))
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUnixAddr("unix", abs)
	if err != nil {
		return nil, err
	}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}
	if runtimeDir != "" {
		if err := os.Chmod(abs, 0o755|os.ModeSticky); err != nil {
			return nil, err
		}
	}
	return &Socket{
		l:     l,
		rmdir: true,
	}, nil
}

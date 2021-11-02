// Copyright (C) 2014 Space Monkey, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package spacelog

import (
	"golang.org/x/sys/unix"
)

// CaptureOutputToFd redirects the current process' stdout and stderr file
// descriptors to the given file descriptor, using the dup2 syscall.
func CaptureOutputToFd(fd int) error {
	err := unix.Dup2(fd, unix.Stdout)
	if err != nil {
		return err
	}
	err = unix.Dup2(fd, unix.Stderr)
	if err != nil {
		return err
	}
	return nil
}

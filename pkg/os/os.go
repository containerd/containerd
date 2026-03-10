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

package os

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/moby/sys/symlink"

	"github.com/containerd/containerd/v2/core/mount"
)

// OS collects system level operations that need to be mocked out
// during tests.
type OS interface {
	MkdirAll(path string, perm os.FileMode) error
	RemoveAll(path string) error
	Stat(ctx context.Context, name string) error
	ResolveSymbolicLink(name string) (string, error)
	FollowSymlinkInScope(path, scope string) (string, error)
	CopyFile(src, dest string, perm os.FileMode) error
	WriteFile(filename string, data []byte, perm os.FileMode) error
	Hostname() (string, error)
	Mount(source string, target string, fstype string, flags uintptr, data string) error
	Unmount(target string) error
	LookupMount(path string) (mount.Info, error)
}

// RealOS is used to dispatch the real system level operations.
type RealOS struct{}

// MkdirAll will call os.MkdirAll to create a directory.
func (RealOS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// RemoveAll will call os.RemoveAll to remove the path and its children.
func (RealOS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// Stat will execute "stat" to verify if the given path exists.
// A timeout is enforced to prevent the call from getting stuck when the
// underlying path is non-responsive.
// If the context does not have a deadline set, then a default 5 sec timeout
// will be used while calling stat.
func (RealOS) Stat(ctx context.Context, name string) error {
	var cancel context.CancelFunc
	if _, hasDeadlineSet := ctx.Deadline(); !hasDeadlineSet {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "stat", name)
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil { // Context expired
			return ctx.Err()
		}

		// If stat exits with a non-zero status (usually 1), it means Not Found.
		// In some environments, if a path is restricted, it might also return 1.
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return os.ErrNotExist
			}
			return fmt.Errorf("stat failed with %v: %w", exitErr.ExitCode(), err)
		}
		return err
	}
	return nil
}

// FollowSymlinkInScope will call symlink.FollowSymlinkInScope.
func (RealOS) FollowSymlinkInScope(path, scope string) (string, error) {
	return symlink.FollowSymlinkInScope(path, scope)
}

// CopyFile will copy src file to dest file
func (RealOS) CopyFile(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// WriteFile will call os.WriteFile to write data into a file.
func (RealOS) WriteFile(filename string, data []byte, perm os.FileMode) error {
	return os.WriteFile(filename, data, perm)
}

// Hostname will call os.Hostname to get the hostname of the host.
func (RealOS) Hostname() (string, error) {
	return os.Hostname()
}

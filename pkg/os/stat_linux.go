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

package os

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// Stat executes "stat" to verify if the given path exists. A timeout is enforced
// to prevent the call from getting stuck when the underlying path is
// non-responsive. If the path is responsive, it falls back to Go's os.Stat to
// return exact Go error types (e.g. os.ErrNotExist, os.ErrPermission).
// If the context does not have a deadline set, then a default 5 sec timeout is
// used.
func (RealOS) Stat(ctx context.Context, name string) error {
	if _, hasDeadlineSet := ctx.Deadline(); !hasDeadlineSet {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "stat", name)
	err := cmd.Run()
	if err == nil {
		return nil
	}

	// If the context timed out or was cancelled, there's a problem with the mount.
	// exec.CommandContext terminated the child process, so no goroutines are leaked.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Since cmd.Run() completed without timing out, the filesystem is responsive.
	// Call Go's os.Stat directly to return the exact standard Go error type.
	_, statErr := os.Stat(name)
	return statErr
}

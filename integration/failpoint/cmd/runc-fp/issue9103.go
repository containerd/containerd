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

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// issue9103KillInitAfterCreate kills the runc.Init process after creating
// command returns successfully.
//
// REF: https://github.com/containerd/containerd/issues/9103
func issue9103KillInitAfterCreate(ctx context.Context, method invoker) error {
	isCreated := strings.Contains(strings.Join(os.Args, ","), ",create,")

	if err := method(ctx); err != nil {
		return err
	}

	if !isCreated {
		return nil
	}

	initPidPath := "init.pid"
	data, err := os.ReadFile(initPidPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", initPidPath, err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return fmt.Errorf("failed to get init pid from string %s: %w", string(data), err)
	}

	if pid <= 0 {
		return fmt.Errorf("unexpected init pid %v", pid)
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to kill the init pid %v: %w", pid, err)
	}

	// Ensure that the containerd-shim has received the SIGCHLD and start
	// to cleanup
	time.Sleep(3 * time.Second)
	return nil
}

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

//go:build windows

package v2

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"github.com/containerd/log"
	"golang.org/x/sys/windows"
)

// runShimStart executes the shim start command inside a Windows Job Object.
// The Job Object is configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so
// that closing the handle terminates the entire process tree (shim + VM).
// Child processes of the shim inherit Job Object membership automatically.
//
// The cleanup function is stored on b.bundle.Cleanup so that both
// binary.Delete (dead-shim path) and shim.Delete (graceful path) can
// close the Job Object before attempting to remove the bundle directory.
func (b *binary) runShimStart(cmd *exec.Cmd) ([]byte, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.L.WithError(err).Warn("failed to create job object for shim, falling back to default start")
		return cmd.CombinedOutput()
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		log.L.WithError(err).Warn("failed to configure job object for shim, falling back to default start")
		return cmd.CombinedOutput()
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return buf.Bytes(), err
	}

	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		// Process may have already exited — continue without job assignment.
		windows.CloseHandle(job)
		log.L.WithError(err).Warn("failed to open shim process handle for job object assignment")
	} else {
		if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
			windows.CloseHandle(processHandle)
			windows.CloseHandle(job)
			log.L.WithError(err).Warn("failed to assign shim process to job object")
		} else {
			windows.CloseHandle(processHandle)
			var once sync.Once
			b.bundle.Cleanup = func() {
				once.Do(func() {
					log.L.WithField("bundle", b.bundle.ID).Debug("closing shim job object")
					if err := windows.CloseHandle(job); err != nil {
						log.L.WithError(err).WithField("bundle", b.bundle.ID).Warn("failed to close shim job object")
					}
				})
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return buf.Bytes(), fmt.Errorf("%s: %w", buf.Bytes(), err)
	}

	return buf.Bytes(), nil
}

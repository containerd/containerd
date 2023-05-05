//go:build windows

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

package bindir

import (
	"context"
	"fmt"
	"os/exec"
	"unsafe"

	"github.com/containerd/containerd/log"
	"golang.org/x/sys/windows"
)

type process struct {
	cmd *exec.Cmd

	jobHandle     *windows.Handle
	processHandle *windows.Handle
}

// Configure the verifier command so that killing it kills all child
// processes of the verifier process.
//
// Job/process management based on:
// https://devblogs.microsoft.com/oldnewthing/20131209-00/?p=2433
func startProcess(ctx context.Context, cmd *exec.Cmd) (*process, error) {
	p := &process{
		cmd: cmd,
	}

	jobHandle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("creating job object: %w", err)
	}
	p.jobHandle = &jobHandle

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		p.cleanup(ctx)
		return nil, fmt.Errorf("setting limits for job object: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.cleanup(ctx)
		return nil, fmt.Errorf("starting process: %w", err)
	}

	processHandle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return nil, fmt.Errorf("getting handle for verifier process: %w", err)
	}
	p.processHandle = &processHandle

	err = windows.AssignProcessToJobObject(jobHandle, processHandle)
	if err != nil {
		p.cleanup(ctx)
		return nil, fmt.Errorf("associating new process to job object: %w", err)
	}

	return p, nil
}

func (p *process) cleanup(ctx context.Context) {
	if p.jobHandle != nil {
		if err := windows.CloseHandle(*p.jobHandle); err != nil {
			log.G(ctx).WithError(err).Error("failed to close job handle")
		}
	}
	if p.processHandle != nil {
		if err := windows.CloseHandle(*p.processHandle); err != nil {
			log.G(ctx).WithError(err).Error("failed to close process handle")
		}
	}
}

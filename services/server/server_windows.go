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

package server

import (
	"context"
	"unsafe"

	"github.com/containerd/containerd/log"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/containerd/ttrpc"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

func getPriorityClass(priorityClassName string) uint32 {
	var priorityClassMap = map[string]uint32{
		"IDLE_PRIORITY_CLASS":         uint32(windows.IDLE_PRIORITY_CLASS),
		"BELOW_NORMAL_PRIORITY_CLASS": uint32(windows.BELOW_NORMAL_PRIORITY_CLASS),
		"NORMAL_PRIORITY_CLASS":       uint32(windows.NORMAL_PRIORITY_CLASS),
		"ABOVE_NORMAL_PRIORITY_CLASS": uint32(windows.ABOVE_NORMAL_PRIORITY_CLASS),
		"HIGH_PRIORITY_CLASS":         uint32(windows.HIGH_PRIORITY_CLASS),
		"REALTIME_PRIORITY_CLASS":     uint32(windows.REALTIME_PRIORITY_CLASS),
	}
	return priorityClassMap[priorityClassName]
}

// addCurrentProcessToJobObjectAndSetPriorityClass creates a new Job Object
// (https://docs.microsoft.com/en-us/windows/win32/procthread/job-objects),
// adds the current process to the job object, and specifies the priority
// class for the job object to the specified value.
// A job object is used here so that any spawned processes such as CNI or
// shim binaries are created at the specified thread priority class.
// Running containerd and shim binaries with above normal / high priority
// can help improve responsiveness on machines with high CPU utilization.
func addCurrentProcessToJobObjectAndSetPriorityClass(pc uint32) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limitInfo := windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
		LimitFlags:    windows.JOB_OBJECT_LIMIT_PRIORITY_CLASS,
		PriorityClass: pc}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectBasicLimitInformation,
		uintptr(unsafe.Pointer(&limitInfo)),
		uint32(unsafe.Sizeof(limitInfo))); err != nil {
		return err
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		return err
	}
	return nil
}

func apply(ctx context.Context, config *srvconfig.Config) error {
	if config.WindowsPriorityClass != "" {
		log.G(ctx).Infof("Setting process priority class to %s", config.WindowsPriorityClass)

		pc := getPriorityClass(config.WindowsPriorityClass)
		if pc == 0 {
			return errors.Errorf("Invalid priority class %s, valid priority classes are defined at "+
				"https://docs.microsoft.com/en-us/windows/win32/procthread/scheduling-priorities", config.WindowsPriorityClass)
		}
		if err := addCurrentProcessToJobObjectAndSetPriorityClass(pc); err != nil {
			log.G(ctx).Error(err)
			return err
		}
	}

	return nil
}

func newTTRPCServer() (*ttrpc.Server, error) {
	return ttrpc.NewServer()
}

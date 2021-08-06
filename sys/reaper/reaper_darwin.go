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

package reaper

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/pkg/process"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// reapOS is additional reap process upon receipt of SIGCHLD.
// Since macOS doesn't raise SIGCHLD on orphaned children's exit,
// reapOS polls the status of registered process and terminate it
// if it's already exited.
func reapOS(exits []exit) ([]exit, error) {
	pid, err := runc.ReadPidFile(filepath.Join("", process.InitPidFile))
	if pid <= 0 {
		return exits, errors.Errorf("can't find pid=%d %s", pid, err)
	}

	process, err := os.FindProcess(pid)
	// ensure the process is running
	if process != nil {
		// from kill(2):
		// A value of 0, however, will cause error checking to be
		// performed (with no signal being sent).
		// This can be used to check the validity of pid.
		err = process.Signal(syscall.Signal(0))
	}
	logrus.Debugf("checking pid=%d proc=%v err=%v", pid, process, err)

	// if process exists && already finished
	if err != nil && strings.Contains(err.Error(), "os: process already finished") {
		exits = append(exits, exit{
			Pid:    pid,
			Status: 0, // XXX
		})

		logrus.Debugf("reapOS: detect exited, pid=%d", pid)
	}

	return exits, nil
}

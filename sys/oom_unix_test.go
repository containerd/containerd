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

package sys

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestSetPositiveOomScoreAdjustment(t *testing.T) {
	adjustment, err := adjustOom(123)
	if err != nil {
		t.Error(err)
		return
	}
	assert.Check(t, is.Equal(adjustment, 123))
}

func TestSetNegativeOomScoreAdjustmentWhenPrivileged(t *testing.T) {
	if RunningUnprivileged() {
		t.Skip("Needs to be run as root")
		return
	}

	adjustment, err := adjustOom(-123)
	if err != nil {
		t.Error(err)
		return
	}
	assert.Check(t, is.Equal(adjustment, -123))
}

func TestSetNegativeOomScoreAdjustmentWhenUnprivilegedHasNoEffect(t *testing.T) {
	if RunningPrivileged() {
		t.Skip("Needs to be run as non-root")
		return
	}

	adjustment, err := adjustOom(-123)
	if err != nil {
		t.Error(err)
		return
	}
	assert.Check(t, is.Equal(adjustment, 0))
}

func adjustOom(adjustment int) (int, error) {
	cmd := exec.Command("sleep", "100")
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	defer cmd.Process.Kill()

	pid, err := waitForPid(cmd.Process)
	if err != nil {
		return 0, err
	}

	if err := SetOOMScore(pid, adjustment); err != nil {
		return 0, err
	}

	return GetOOMScoreAdj(pid)
}

func waitForPid(process *os.Process) (int, error) {
	c := make(chan int, 1)
	go func() {
		for {
			pid := process.Pid
			if pid != 0 {
				c <- pid
			}
		}
	}()

	select {
	case pid := <-c:
		return pid, nil
	case <-time.After(10 * time.Second):
		return 0, errors.New("process did not start in 10 seconds")
	}
}

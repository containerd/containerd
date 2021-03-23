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

	"github.com/containerd/containerd/pkg/userns"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestSetPositiveOomScoreAdjustment(t *testing.T) {
	// Setting a *positive* OOM score adjust does not require privileged
	_, adjustment, err := adjustOom(123)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(adjustment, 123))
}

func TestSetNegativeOomScoreAdjustmentWhenPrivileged(t *testing.T) {
	if !runningPrivileged() || userns.RunningInUserNS() {
		t.Skip("requires root and not running in user namespace")
		return
	}

	_, adjustment, err := adjustOom(-123)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(adjustment, -123))
}

func TestSetNegativeOomScoreAdjustmentWhenUnprivilegedHasNoEffect(t *testing.T) {
	if runningPrivileged() && !userns.RunningInUserNS() {
		t.Skip("needs to be run as non-root or in user namespace")
		return
	}

	initial, adjustment, err := adjustOom(-123)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(adjustment, initial))
}

func adjustOom(adjustment int) (int, int, error) {
	cmd := exec.Command("sleep", "100")
	if err := cmd.Start(); err != nil {
		return 0, 0, err
	}

	defer cmd.Process.Kill()

	pid, err := waitForPid(cmd.Process)
	if err != nil {
		return 0, 0, err
	}
	initial, err := GetOOMScoreAdj(pid)
	if err != nil {
		return 0, 0, err
	}

	if err := SetOOMScore(pid, adjustment); err != nil {
		return 0, 0, err
	}

	adj, err := GetOOMScoreAdj(pid)
	return initial, adj, err
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

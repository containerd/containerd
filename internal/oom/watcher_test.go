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

package oom

import (
	"fmt"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/containerd/v2/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var defaultCgroup2Path = "/sys/fs/cgroup"

func TestWatcher(t *testing.T) {
	testutil.RequiresRoot(t)

	skipIfCgroupUnavailable(t)
	skipIfBinaryUnavailable(t, "dd")

	group := fmt.Sprintf("/%s", t.Name())
	mgr, err := cgroupsv2.NewManager(defaultCgroup2Path, group, &cgroupsv2.Resources{})
	require.NoError(t, err)

	ddCmd := exec.Command("dd", "if=/dev/zero", "of=/dev/null bs=20M")
	err = ddCmd.Start()
	defer func() {
		ddCmd.Wait()
	}()
	require.NoError(t, err)

	require.NoError(t, mgr.AddProc(uint64(ddCmd.Process.Pid)))

	var oomKills atomic.Uint64

	watchers := New()
	containerID := "dd-test"
	require.NoError(t, watchers.Add(containerID, ddCmd.Process.Pid, func(cid string) {
		assert.Equal(t, containerID, cid)
		oomKills.Add(1)
	}))

	require.NoError(t, mgr.Update(&cgroupsv2.Resources{
		Memory: &cgroupsv2.Memory{
			Max:  toPtr(int64(15 * 1024 * 1024)),
			Swap: toPtr(int64(15 * 1024 * 1024)),
		},
	}))
	defer func() {
		watchers.Stop(containerID)
	}()

	err = ddCmd.Wait()
	require.ErrorContains(t, err, "signal: killed")

	require.Eventuallyf(t, func() bool {
		return oomKills.Load() == uint64(1)
	}, 30*time.Second, time.Second, "should receive oom event (%v)", oomKills.Load())

	require.NoError(t, watchers.Stop(containerID))
}

func skipIfCgroupUnavailable(t *testing.T) {
	if mode := cgroups.Mode(); mode != cgroups.Unified {
		t.Skipf("skip because it's not cgroup v2 (mode: %v)", mode)
	}
}

func skipIfBinaryUnavailable(t *testing.T, binaryName string) {
	_, err := exec.LookPath(binaryName)
	if err != nil {
		t.Skipf("skip because %s is not available (err: %v)", binaryName, err)
	}
}

// TestStopRemovesWatcher verifies that Stop removes the container ID from the
// internal map so the same ID can be re-added after a stop. This is the
// Kubernetes restart scenario: the shim process is reused across container
// restarts, so the same container ID will be passed to Add again.
func TestStopRemovesWatcher(t *testing.T) {
	testutil.RequiresRoot(t)
	skipIfCgroupUnavailable(t)

	group := fmt.Sprintf("/%s", t.Name())
	mgr, err := cgroupsv2.NewManager(defaultCgroup2Path, group, &cgroupsv2.Resources{})
	require.NoError(t, err)
	defer mgr.Delete()

	sleepCmd := exec.Command("sleep", "3600")
	require.NoError(t, sleepCmd.Start())
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	require.NoError(t, mgr.AddProc(uint64(sleepCmd.Process.Pid)))

	watchers := New()
	containerID := "restart-test"

	require.NoError(t, watchers.Add(containerID, sleepCmd.Process.Pid, func(string) {}))
	require.NoError(t, watchers.Stop(containerID))

	// After Stop, the same container ID must be re-addable.
	// Before the fix this returned errdefs.ErrAlreadyExists.
	err = watchers.Add(containerID, sleepCmd.Process.Pid, func(string) {})
	require.NoError(t, err, "re-adding container after Stop should succeed")

	require.NoError(t, watchers.Stop(containerID))
}

func toPtr[T comparable](v T) *T {
	return &v
}

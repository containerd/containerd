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
	"errors"
	"fmt"
	"os"
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

// newTestWatcher builds a watcher without starting its goroutine, so a test
// controls exactly if and when errCh is resolved.
func newTestWatcher(t *testing.T, cid string) *watcher {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		r.Close()
		w.Close()
	})

	return &watcher{
		cid:     cid,
		eventFD: r,
		errCh:   make(chan error, 1),
	}
}

// TestWatcherStopDoesNotBlockForever covers the shim wedge reported in #13814.
// The shim calls Stop from handleProcessExit before it publishes TaskExit, on
// its single processExits goroutine, so a watcher goroutine which never comes
// back used to stop that shim from ever publishing another task exit: clients
// waiting on the task, the container IO fifos and the CRI exit monitors then
// all leak, and the shim is orphaned with its container already gone.
func TestWatcherStopDoesNotBlockForever(t *testing.T) {
	// errCh is never written to and never closed, which is how a watcher
	// goroutine parked in a read of the container's cgroup files looks to stop.
	w := newTestWatcher(t, "stuck-container")

	done := make(chan error, 1)
	go func() {
		done <- w.stop()
	}()

	select {
	case err := <-done:
		require.ErrorContains(t, err, "timed out")
	case <-time.After(stopTimeout + 30*time.Second):
		t.Fatal("watcher.stop never returned: the shim would stop publishing task exits")
	}
}

// TestWatcherStopReportsWatcherError makes sure bounding the wait did not stop
// stop from reporting what the watcher goroutine actually failed with.
func TestWatcherStopReportsWatcherError(t *testing.T) {
	w := newTestWatcher(t, "failed-container")

	watcherErr := errors.New("read memory.events: boom")
	w.errCh <- watcherErr
	close(w.errCh)

	require.ErrorIs(t, w.stop(), watcherErr)
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

func toPtr[T comparable](v T) *T {
	return &v
}

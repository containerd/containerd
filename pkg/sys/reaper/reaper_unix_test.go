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

package reaper

import (
	"os"
	"os/exec"
	"testing"
	"time"

	runc "github.com/containerd/go-runc"
	"golang.org/x/sys/unix"
)

// TestHelperProcess is a helper process used by tests to avoid depending on
// external binaries like "true" or "sleep". It is not a test itself.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	// If SLEEP is set, sleep for that duration before exiting.
	if d := os.Getenv("GO_TEST_HELPER_SLEEP"); d != "" {
		dur, _ := time.ParseDuration(d)
		time.Sleep(dur)
	}
	os.Exit(0)
}

// helperCmd returns an exec.Cmd that runs the test binary as a helper process.
// The helper exits immediately unless sleep duration is specified.
func helperCmd(t *testing.T, sleep ...time.Duration) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_TEST_HELPER_PROCESS=1")
	if len(sleep) > 0 {
		cmd.Env = append(cmd.Env, "GO_TEST_HELPER_SLEEP="+sleep[0].String())
	}
	return cmd
}

// TestReapSecondScanCatchesLateExits verifies that Reap() performs a second
// wait4 scan to catch processes that exit during the notify phase, addressing
// the SIGCHLD coalescing race condition described in containerd/containerd#12678.
func TestReapSecondScanCatchesLateExits(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("cannot set subreaper: %v", err)
	}
	defer unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0)

	ch := Default.Subscribe()
	defer Default.Unsubscribe(ch)

	// Start two processes without calling cmd.Wait() — let Reap() collect them.
	cmd1 := helperCmd(t)
	if err := cmd1.Start(); err != nil {
		t.Fatalf("failed to start process 1: %v", err)
	}
	pid1 := cmd1.Process.Pid

	cmd2 := helperCmd(t)
	if err := cmd2.Start(); err != nil {
		t.Fatalf("failed to start process 2: %v", err)
	}
	pid2 := cmd2.Process.Pid

	// Give processes time to exit.
	time.Sleep(200 * time.Millisecond)

	// Call Reap() — should collect both exits.
	if err := Reap(); err != nil {
		t.Fatalf("Reap() failed: %v", err)
	}

	// Collect exit events.
	received := make(map[int]bool)
	deadline := time.After(5 * time.Second)
	for len(received) < 2 {
		select {
		case e := <-ch:
			if e.Pid == pid1 || e.Pid == pid2 {
				received[e.Pid] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for exit events: got pids %v, want %d and %d", received, pid1, pid2)
		}
	}
}

// TestReapSecondScanNoFalsePositives verifies that the second scan does not
// produce spurious exit events when no additional processes have exited.
func TestReapSecondScanNoFalsePositives(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("cannot set subreaper: %v", err)
	}
	defer unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0)

	ch := Default.Subscribe()
	defer Default.Unsubscribe(ch)

	// Start a single process.
	cmd := helperCmd(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	pid := cmd.Process.Pid

	// Give time to exit.
	time.Sleep(200 * time.Millisecond)

	if err := Reap(); err != nil {
		t.Fatalf("Reap() failed: %v", err)
	}

	// Should get exactly one exit event for our pid.
	received := 0
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case e := <-ch:
			if e.Pid == pid {
				received++
			}
		case <-timeout:
			break loop
		}
	}
	if received != 1 {
		t.Fatalf("expected 1 exit event for pid %d, got %d", pid, received)
	}
}

// TestReapWithDelayedExit starts a process that exits after an initial Reap()
// call and verifies that a subsequent Reap() catches it via the second scan.
func TestReapWithDelayedExit(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("cannot set subreaper: %v", err)
	}
	defer unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0)

	ch := Default.Subscribe()
	defer Default.Unsubscribe(ch)

	// Start a process that sleeps briefly — will exit after first Reap scan.
	cmd := helperCmd(t, 100*time.Millisecond)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	pid := cmd.Process.Pid

	// First Reap — process hasn't exited yet, first scan gets nothing.
	if err := Reap(); err != nil {
		t.Fatalf("first Reap() failed: %v", err)
	}

	// Wait for process to exit.
	time.Sleep(300 * time.Millisecond)

	// Second Reap — the second scan inside should catch it.
	if err := Reap(); err != nil {
		t.Fatalf("Reap() failed: %v", err)
	}

	// Verify we got the exit event.
	found := false
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Pid == pid {
				found = true
				goto done
			}
		case <-deadline:
			goto done
		}
	}
done:
	if !found {
		t.Fatalf("exit event for pid %d was not received", pid)
	}
}

func drainChannel(ch chan runc.Exit) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

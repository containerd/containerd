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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/pkg/testutil"
)

// childEnv makes the test binary run as the init instead of the tests.
const childEnv = "CONTAINERD_SANDBOX_INIT_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		run()
	}
	os.Exit(m.Run())
}

// TestSignals runs the init as PID 1 of a new PID namespace and sends it
// every signal but SIGKILL and SIGSTOP, from the parent namespace: a signal
// that ended it here would end it from inside the pod as well (see the
// package doc).
func TestSignals(t *testing.T) {
	testutil.RequiresRoot(t)

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWPID}
	require.NoError(t, cmd.Start())
	// waitErr is valid once exited is closed.
	var waitErr error
	exited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	pid := cmd.Process.Pid

	// The setup is done once the last signal of the loop, 64, is ignored.
	require.Eventually(t, func() bool {
		ignored, _ := signalMasks(t, pid)
		return ignored&(1<<63) != 0
	}, 10*time.Second, 10*time.Millisecond, "the init did not finish its signal setup")

	ignored, caught := signalMasks(t, pid)
	assert.NotZero(t, ignored&(1<<(unix.SIGCHLD-1)), "SIGCHLD must be ignored for orphans to be reaped")
	for _, sig := range []syscall.Signal{unix.SIGILL, unix.SIGTRAP, unix.SIGBUS, unix.SIGFPE, unix.SIGSEGV, unix.SIGSTKFLT, unix.SIGSYS} {
		assert.Zero(t, caught&(1<<(sig-1)), "%s must have no handler", unix.SignalName(sig))
	}

	for n := 1; n <= 64; n++ {
		sig := syscall.Signal(n)
		if sig == unix.SIGKILL || sig == unix.SIGSTOP {
			continue
		}
		require.NoError(t, unix.Kill(pid, sig), "signal %d", n)
		select {
		case <-exited:
			t.Fatalf("signal %d (%s) ended the init: %v", n, unix.SignalName(sig), waitErr)
		case <-time.After(20 * time.Millisecond):
		}
	}

	require.NoError(t, unix.Kill(pid, unix.SIGKILL))
	select {
	case <-exited:
		var exitErr *exec.ExitError
		require.ErrorAs(t, waitErr, &exitErr)
		assert.Equal(t, unix.SIGKILL, exitErr.Sys().(syscall.WaitStatus).Signal())
	case <-time.After(5 * time.Second):
		t.Fatal("SIGKILL must end the init")
	}
}

// signalMasks reads the ignored and caught signal sets of pid from /proc;
// bit n-1 stands for signal n.
func signalMasks(t *testing.T, pid int) (ignored, caught uint64) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	require.NoError(t, err)
	for _, line := range strings.Split(string(data), "\n") {
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch field {
		case "SigIgn":
			ignored, err = strconv.ParseUint(value, 16, 64)
			require.NoError(t, err)
		case "SigCgt":
			caught, err = strconv.ParseUint(value, 16, 64)
			require.NoError(t, err)
		}
	}
	return ignored, caught
}

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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// initUser is the user the shim runs the init as.
const initUser = 65535

// TestSignals runs the init as PID 1 of a new PID namespace, as the user the
// shim runs it as, and sends it every signal but SIGKILL and SIGSTOP, from
// the parent namespace: a signal that ended it here would end it from inside
// the pod as well (see the package doc).
func TestSignals(t *testing.T) {
	testutil.RequiresRoot(t)

	cmd := exec.Command(initCopy(t))
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: unix.CLONE_NEWPID,
		Credential: &syscall.Credential{Uid: initUser, Gid: initUser},
	}
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
	for _, sig := range []syscall.Signal{unix.SIGCHLD, unix.SIGURG, unix.SIGPROF} {
		assert.NotZero(t, ignored&(1<<(sig-1)), "%s must be ignored", unix.SignalName(sig))
	}
	assert.Zero(t, caught, "no signal may have a handler (SigCgt %#x)", caught)

	// The /proc/<pid> entries of a process that is not dumpable belong to
	// root (the directory itself keeps the owner of the process).
	var st unix.Stat_t
	require.NoError(t, unix.Stat(fmt.Sprintf("/proc/%d/status", pid), &st))
	assert.EqualValues(t, 0, st.Uid, "the init must not be dumpable")

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

// initCopy copies the test binary where the init user can run it: the
// directories go test builds it in are not traversable for other users.
func initCopy(t *testing.T) string {
	self, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	for d := dir; d != os.TempDir(); d = filepath.Dir(d) {
		require.NoError(t, os.Chmod(d, 0o755))
	}
	src, err := os.Open(self)
	require.NoError(t, err)
	defer src.Close()
	path := filepath.Join(dir, "init")
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o755)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, errors.Join(err, dst.Close()))
	return path
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

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

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/pkg/stdio"
	runc "github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPauseFailureReconcilesRuntimeState(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     bool
		wantStatus string
	}{
		{name: "fails before freezing", wantStatus: "running"},
		{name: "fails after freezing", change: true, wantStatus: "paused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, dir := newPauseTestProcess(t, "running")
			writePauseTestControl(t, dir, "pause-error")
			if !test.change {
				writePauseTestControl(t, dir, "pause-no-effect")
			}

			require.ErrorContains(t, p.Pause(t.Context()), "OCI runtime pause failed")
			require.True(t, p.pauseStateUnknown)
			status, err := p.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, status)
			require.False(t, p.pauseStateUnknown)
			if test.change {
				require.NoError(t, p.Resume(t.Context()))
				state, err := os.ReadFile(filepath.Join(dir, "state"))
				require.NoError(t, err)
				require.Equal(t, "running", string(state))
			}
		})
	}
}

func TestCanceledPauseCanBeResumed(t *testing.T) {
	p, dir := newPauseTestProcess(t, "running")
	readyPath := filepath.Join(dir, "pause-ready")
	require.NoError(t, unix.Mkfifo(readyPath, 0o600))
	ready, err := os.OpenFile(readyPath, os.O_RDWR|unix.O_NONBLOCK, 0)
	require.NoError(t, err)
	defer ready.Close()
	require.NoError(t, ready.SetReadDeadline(time.Now().Add(5*time.Second)))
	writePauseTestControl(t, dir, "pause-block")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	paused := make(chan error, 1)
	go func() { paused <- p.Pause(ctx) }()

	// The runtime signals after changing its simulated freezer state. Cancel
	// only then, so the failed command has a deterministic side effect.
	_, err = ready.Read(make([]byte, 1))
	require.NoError(t, err)
	cancel()
	select {
	case err := <-paused:
		require.ErrorContains(t, err, "OCI runtime pause failed")
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not return after cancellation")
	}

	// Resume must reconcile with a fresh context even without a Status call.
	require.NoError(t, p.Resume(t.Context()))
	status, err := p.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "running", status)
	state, err := os.ReadFile(filepath.Join(dir, "state"))
	require.NoError(t, err)
	require.Equal(t, "running", string(state))
}

func TestFailedPauseStateLookupCanBeRetried(t *testing.T) {
	p, dir := newPauseTestProcess(t, "running")
	writePauseTestControl(t, dir, "pause-error")
	writePauseTestControl(t, dir, "state-error")
	require.Error(t, p.Pause(t.Context()))

	status, err := p.Status(t.Context())
	require.ErrorContains(t, err, "failed to reconcile task pause state")
	require.Empty(t, status)
	require.True(t, p.pauseStateUnknown)
	require.Error(t, p.Resume(t.Context()))
	require.Error(t, p.Pause(t.Context()))
	_, err = p.Exec(t.Context(), "", nil)
	require.Error(t, err)
	require.Error(t, p.Checkpoint(t.Context(), nil))
	require.True(t, p.pauseStateUnknown)

	require.NoError(t, os.Remove(filepath.Join(dir, "state-error")))
	status, err = p.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "paused", status)
	require.NoError(t, p.Resume(t.Context()))
}

func TestResumeFailureReconcilesRuntimeState(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     bool
		wantStatus string
	}{
		{name: "fails before thawing", wantStatus: "paused"},
		{name: "fails after thawing", change: true, wantStatus: "running"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, dir := newPauseTestProcess(t, "paused")
			writePauseTestControl(t, dir, "resume-error")
			if !test.change {
				writePauseTestControl(t, dir, "resume-no-effect")
			}

			require.ErrorContains(t, p.Resume(t.Context()), "OCI runtime resume failed")
			status, err := p.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, status)
			require.False(t, p.pauseStateUnknown)
			if !test.change {
				require.NoError(t, os.Remove(filepath.Join(dir, "resume-error")))
				require.NoError(t, os.Remove(filepath.Join(dir, "resume-no-effect")))
				require.NoError(t, p.Resume(t.Context()))
			}
		})
	}
}

func TestExitClearsUncertainPauseState(t *testing.T) {
	p, dir := newPauseTestProcess(t, "running")
	writePauseTestControl(t, dir, "pause-error")
	writePauseTestControl(t, dir, "state-error")
	require.Error(t, p.Pause(t.Context()))
	p.Platform = pauseTestPlatform{}
	p.SetExited(1)
	status, err := p.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "stopped", status)
	require.False(t, p.pauseStateUnknown)
	// The failed pause froze the runtime, so exit handling must thaw it.
	state, err := os.ReadFile(filepath.Join(dir, "state"))
	require.NoError(t, err)
	require.Equal(t, "running", string(state))
}

func TestUnsettledRuntimeRejectsPauseStateOperations(t *testing.T) {
	for _, shimStatus := range []string{"running", "paused"} {
		for _, runtimeStatus := range []string{"pausing", "stopped"} {
			t.Run(shimStatus+"/"+runtimeStatus, func(t *testing.T) {
				p, dir := newPauseTestProcess(t, shimStatus)
				if shimStatus == "running" {
					writePauseTestControl(t, dir, "pause-error")
					require.Error(t, p.Pause(t.Context()))
				} else {
					writePauseTestControl(t, dir, "resume-error")
					require.Error(t, p.Resume(t.Context()))
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "state"), []byte(runtimeStatus), 0o600))

				status, err := p.Status(t.Context())
				require.ErrorContains(t, err, "cannot reconcile task pause state")
				require.Empty(t, status)
				require.ErrorContains(t, p.Pause(t.Context()), "cannot reconcile task pause state")
				require.ErrorContains(t, p.Resume(t.Context()), "cannot reconcile task pause state")
				_, err = p.Exec(t.Context(), "", nil)
				require.ErrorContains(t, err, "cannot reconcile task pause state")
				require.ErrorContains(t, p.Checkpoint(t.Context(), nil), "cannot reconcile task pause state")
				require.True(t, p.pauseStateUnknown)

				// None of the rejected operations may change the runtime state.
				state, err := os.ReadFile(filepath.Join(dir, "state"))
				require.NoError(t, err)
				require.Equal(t, runtimeStatus, string(state))
			})
		}
	}
}

func TestExitRetriesFailedThawWithoutRevivingProcess(t *testing.T) {
	for _, initialStatus := range []string{"running", "paused"} {
		t.Run(initialStatus, func(t *testing.T) {
			p, dir := newPauseTestProcess(t, initialStatus)
			if initialStatus == "running" {
				writePauseTestControl(t, dir, "pause-error")
				require.Error(t, p.Pause(t.Context()))
			}
			writePauseTestControl(t, dir, "resume-error")
			writePauseTestControl(t, dir, "resume-no-effect")
			p.SetExited(1)
			require.True(t, p.resumeAfterExit)
			require.False(t, p.pauseStateUnknown)
			require.NoError(t, p.Wait(t.Context()))
			require.Equal(t, 1, p.ExitStatus())
			exitedAt := p.ExitedAt()

			// A failed retry must still report the reaped process as stopped.
			status, err := p.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, "stopped", status)
			require.True(t, p.resumeAfterExit)
			state, err := os.ReadFile(filepath.Join(dir, "state"))
			require.NoError(t, err)
			require.Equal(t, "paused", string(state))

			require.NoError(t, os.Remove(filepath.Join(dir, "resume-error")))
			require.NoError(t, os.Remove(filepath.Join(dir, "resume-no-effect")))
			status, err = p.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, "stopped", status)
			require.False(t, p.resumeAfterExit)
			require.Equal(t, exitedAt, p.ExitedAt())
			state, err = os.ReadFile(filepath.Join(dir, "state"))
			require.NoError(t, err)
			require.Equal(t, "running", string(state))
			require.Error(t, p.Pause(t.Context()))
			require.Error(t, p.Resume(t.Context()))
			_, err = p.Exec(t.Context(), "", nil)
			require.Error(t, err)
			require.Error(t, p.Checkpoint(t.Context(), nil))
		})
	}
}

func TestDeleteRetriesFailedExitThaw(t *testing.T) {
	p, dir := newPauseTestProcess(t, "paused")
	writePauseTestControl(t, dir, "resume-error")
	writePauseTestControl(t, dir, "resume-no-effect")
	writePauseTestControl(t, dir, "delete-error")
	p.SetExited(1)

	// Keep the thaw retry when deletion fails too.
	require.ErrorContains(t, p.Delete(t.Context()), "failed to delete task")
	require.True(t, p.resumeAfterExit)
	require.IsType(t, &stoppedState{}, p.initState)

	require.NoError(t, os.Remove(filepath.Join(dir, "resume-error")))
	require.NoError(t, os.Remove(filepath.Join(dir, "resume-no-effect")))
	require.NoError(t, os.Remove(filepath.Join(dir, "delete-error")))
	require.NoError(t, p.Delete(t.Context()))
	require.False(t, p.resumeAfterExit)
	require.IsType(t, &deletedState{}, p.initState)
	state, err := os.ReadFile(filepath.Join(dir, "state"))
	require.NoError(t, err)
	require.Equal(t, "running", string(state))
}

func TestDeleteAfterFailedExitThawAllowsMissingRuntime(t *testing.T) {
	p, dir := newPauseTestProcess(t, "paused")
	writePauseTestControl(t, dir, "resume-error")
	writePauseTestControl(t, dir, "resume-no-effect")
	writePauseTestControl(t, dir, "delete-missing")
	p.SetExited(1)

	// Missing runtime state must not prevent releasing shim-owned resources.
	require.NoError(t, p.Delete(t.Context()))
	require.False(t, p.resumeAfterExit)
	require.IsType(t, &deletedState{}, p.initState)
}

type pauseTestPlatform struct{ stdio.Platform }

func (pauseTestPlatform) ShutdownConsole(context.Context, console.Console) error { return nil }

func writePauseTestControl(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
}

func newPauseTestProcess(t *testing.T, status string) (*Init, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state"), []byte(status), 0o600))
	executable := filepath.Join(dir, "runtime")
	// This executable exercises the real go-runc command and shim state
	// machine. The state file models the runtime's freezer independently.
	script := `#!/bin/sh
case "$1" in
pause)
	[ -e pause-no-effect ] || printf paused > state
	if [ -e pause-block ]; then
		printf x > pause-ready
		while :; do :; done
	fi
	[ ! -e pause-error ]
	;;
resume)
	[ -e resume-no-effect ] || printf running > state
	[ ! -e resume-error ]
	;;
state)
	[ ! -e state-error ] || exit 1
	printf '{"status":"%s"}\n' "$(cat state)"
	;;
delete)
	if [ -e delete-missing ]; then
		printf 'container does not exist\n' >&2
		exit 1
	fi
	[ ! -e delete-error ]
	;;
*) exit 1 ;;
esac
`
	require.NoError(t, os.WriteFile(executable, []byte(script), 0o700))
	p := New("container", &runc.Runc{Command: executable, WorkDir: dir}, stdio.Stdio{})
	p.Platform = pauseTestPlatform{}
	p.Rootfs = filepath.Join(dir, "rootfs")
	require.NoError(t, os.Mkdir(p.Rootfs, 0o700))
	if status == "paused" {
		p.initState = &pausedState{p: p}
	} else {
		p.initState = &runningState{p: p}
	}
	return p, dir
}

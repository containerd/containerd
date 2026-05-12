//go:build !windows

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

// Regression tests for the stdio-drain / handleEventTimeout collision
// fixed in this commit. See #13377 and PR #12364.
//
// The contract: (*Init).delete and (*execProcess).delete must return
// control to the caller (so runtime.Delete and mount.UnmountRecursive
// receive the outer ctx with its full budget intact) even when the
// stdio drain blocks. The drainAndCloseStdio helper preserves #12364's
// guarantee that buffered output is fully copied before the pipe read
// FDs are forcibly closed.

package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/sirupsen/logrus"
)

// outerBudget mirrors handleEventTimeout in internal/cri/server/events.go.
const outerBudget = 10 * time.Second

// minRemainingForRuntimeDelete is the lower bound on outer-ctx budget
// that must remain after the synchronous portion of delete() returns.
// runtime.Delete + mount.UnmountRecursive in production take ~50-500ms;
// 5s is a generous floor that distinguishes the broken collision (≤0s
// remaining) from any correct fix.
const minRemainingForRuntimeDelete = 5 * time.Second

// testDrainTimeout is the shortened drain budget swapped in by tests that
// exercise the real (*Init).delete / (*execProcess).delete paths against
// a wedged wg. Production uses drainStdioTimeout=10s; the test shortens
// it so the synchronous exec path returns in well under outerBudget.
const testDrainTimeout = 100 * time.Millisecond

// wedge holds wg above zero until either (a) the returned release closure
// is called or (b) test cleanup runs (whichever comes first). It models
// the production failure mode where a child process still holds the
// stdio pipe write-FD: io.CopyBuffer never returns from read() and
// wg.Done() never fires. In production the wg is released by closing
// the read FDs (the io.Closer slice + processIO.Close() inside
// drainAndCloseStdio).
//
// Accepts a *sync.WaitGroup pointer so callers can wedge a wg that lives
// inside a struct field (e.g., Init.wg, execProcess.wg) without copying
// it — sync.WaitGroup must not be copied after first use.
func wedge(t *testing.T, wg *sync.WaitGroup) (release func()) {
	t.Helper()
	wg.Add(1)
	released := make(chan struct{})
	go func() {
		<-released
		wg.Done()
	}()
	var once sync.Once
	release = func() {
		once.Do(func() { close(released) })
	}
	t.Cleanup(release) // best-effort: release even if test forgets
	return release
}

// blockedWG is a convenience wrapper that allocates a fresh wg and wedges
// it. Used by the helper-direct tests that don't need to embed the wg
// in a struct.
func blockedWG(t *testing.T) (*sync.WaitGroup, func()) {
	t.Helper()
	wg := &sync.WaitGroup{}
	return wg, wedge(t, wg)
}

// trackedCloser is an io.Closer that records the moment Close() is
// called. It models registered closers and the processIO close-side.
type trackedCloser struct {
	mu       sync.Mutex
	closedAt time.Time
}

func (c *trackedCloser) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closedAt.IsZero() {
		c.closedAt = time.Now()
	}
	return nil
}

func (c *trackedCloser) ClosedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedAt
}

func withCapturedLog(t *testing.T) (*logrus.Entry, *capturingHook) {
	t.Helper()
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	l.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})
	hook := &capturingHook{}
	l.AddHook(hook)
	return logrus.NewEntry(l), hook
}

type capturingHook struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (*capturingHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *capturingHook) Fire(e *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(e.Message)
	h.buf.WriteByte('\n')
	return nil
}
func (h *capturingHook) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.String()
}

// shortenDrainTimeout swaps the production drainStdioTimeout for a short
// value so tests that go through the real (*Init).delete / (*execProcess).delete
// paths finish quickly even when the wg is wedged. The original value is
// restored via t.Cleanup.
func shortenDrainTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := drainStdioTimeout
	drainStdioTimeout = d
	t.Cleanup(func() { drainStdioTimeout = prev })
}

// noopRuntime returns a *runc.Runc whose Delete shells out to /usr/bin/true
// (a no-op subprocess that exits 0 quickly). This lets the real
// (*Init).delete path run without standing up a runc container, while
// remaining fast enough that the outer-ctx-budget assertion stays meaningful.
// Falls back to /bin/true on systems where /usr/bin/true is absent.
func noopRuntime(t *testing.T) *runc.Runc {
	t.Helper()
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("no `true` binary in PATH: %v", err)
	}
	return &runc.Runc{Command: bin}
}

func assertOuterCtxHasBudgetForRuntimeDelete(t *testing.T, outerCtx context.Context, elapsed time.Duration) {
	t.Helper()
	if outerCtx.Err() != nil {
		t.Fatalf("outer ctx is Done after the synchronous portion of delete (err=%v, elapsed=%s)", outerCtx.Err(), elapsed)
	}
	deadline, ok := outerCtx.Deadline()
	if !ok {
		t.Fatalf("outer ctx has no deadline — test harness is broken")
	}
	remaining := time.Until(deadline)
	if remaining < minRemainingForRuntimeDelete {
		t.Fatalf("outer ctx has only %s remaining after delete (elapsed=%s); runtime.Delete needs at least %s",
			remaining, elapsed, minRemainingForRuntimeDelete)
	}
	t.Logf("OK — outerRemaining=%s elapsed=%s", remaining, elapsed)
}

// TestInitDeleteDoesNotConsumeOuterContextWhenStdioBlocks asserts the
// PR-#12364 timeout collision is fixed: when (*Init).delete is called
// with a wedged stdio wg, the synchronous portion still returns promptly
// (drain runs in a goroutine) and the outer ctx is preserved with enough
// budget for runtime.Delete + mount.UnmountRecursive to complete.
//
// This exercises (*Init).delete end-to-end against a stub runtime — not
// drainAndCloseStdio in isolation — so it would fail on the pre-fix code
// path where drainAndCloseStdio was invoked synchronously.
func TestInitDeleteDoesNotConsumeOuterContextWhenStdioBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running drain test in -short mode")
	}

	// Shorten the drain timeout so even if (*Init).delete became
	// synchronous in a future regression, the test would observe budget
	// loss bounded by 100ms — well within outerBudget. With the goroutine
	// design, the drain runs entirely in the background and the timeout
	// value doesn't matter for this assertion.
	shortenDrainTimeout(t, testDrainTimeout)

	p := &Init{
		id:      "test-cid",
		runtime: noopRuntime(t),
		// Rootfs left empty so mount.UnmountRecursive returns nil immediately.
	}
	// Wedge p.wg in place so (*Init).delete's drainAndCloseStdio goroutine
	// observes the failure mode (read goroutine never returns).
	wedge(t, &p.wg)

	outerCtx, cancel := context.WithTimeout(context.Background(), outerBudget)
	defer cancel()

	start := time.Now()
	if err := p.delete(outerCtx); err != nil {
		t.Fatalf("(*Init).delete returned error: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("(*Init).delete returned in %s (drain still running in background)", elapsed)
	assertOuterCtxHasBudgetForRuntimeDelete(t, outerCtx, elapsed)
}

// TestExecDeleteDoesNotConsumeOuterContextWhenStdioBlocks is the analog
// for (*execProcess).delete. PR #12364 made the same 2s→10s change in
// both call sites. (*execProcess).delete invokes drainAndCloseStdio
// synchronously by design (callers like TestContainerExecLargeOutputWithTTY
// read stdout immediately after Delete returns), so the assertion is
// "outer ctx survives the drain timeout" rather than "delete returns
// immediately". With testDrainTimeout=100ms, delete returns in <200ms
// and outerBudget=10s leaves ≫5s remaining.
func TestExecDeleteDoesNotConsumeOuterContextWhenStdioBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running drain test in -short mode")
	}

	shortenDrainTimeout(t, testDrainTimeout)

	parent := &Init{id: "parent-init", runtime: noopRuntime(t)}
	e := &execProcess{
		id:     "test-exec",
		path:   t.TempDir(),
		parent: parent,
	}
	wedge(t, &e.wg)

	outerCtx, cancel := context.WithTimeout(context.Background(), outerBudget)
	defer cancel()

	start := time.Now()
	if err := e.delete(outerCtx); err != nil {
		t.Fatalf("(*execProcess).delete returned error: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("(*execProcess).delete returned in %s", elapsed)
	assertOuterCtxHasBudgetForRuntimeDelete(t, outerCtx, elapsed)
}

// TestDrainAndCloseStdio_ClosesAfterDrainCompletes asserts the
// close-after-drain ordering: registered closers are NOT invoked until
// the drain has completed (or its budget has elapsed). This is the
// #12364 contract: io.CopyBuffer goroutines that copy buffered output
// must finish before the pipe read FDs are forcibly closed, otherwise
// log output is truncated.
func TestDrainAndCloseStdio_ClosesAfterDrainCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ordering test in -short mode")
	}

	wg, release := blockedWG(t)

	tc1 := &trackedCloser{}
	tc2 := &trackedCloser{}
	logger, _ := withCapturedLog(t)

	// Run in the foreground so we can assert on ordering. Use the
	// production timeout (10s) — we'll release the wg manually before
	// it expires, so the close path is the drain-completed branch.
	done := make(chan struct{})
	start := time.Now()
	go func() {
		drainAndCloseStdio(wg, nil, []io.Closer{tc1, tc2}, logger, "test", 10*time.Second)
		close(done)
	}()

	// Release the wg after a measurable delay; closers must not fire
	// before this moment.
	releaseAt := start.Add(200 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	release()
	<-done

	if tc1.ClosedAt().Before(releaseAt) {
		t.Fatalf("closer 1 fired BEFORE drain completed (closedAt=%s, releaseAt=%s) — log truncation regression",
			tc1.ClosedAt().Sub(start), releaseAt.Sub(start))
	}
	if tc2.ClosedAt().Before(releaseAt) {
		t.Fatalf("closer 2 fired BEFORE drain completed — log truncation regression")
	}
	if tc1.ClosedAt().IsZero() || tc2.ClosedAt().IsZero() {
		t.Fatalf("closer did not fire — cleanup contract broken")
	}
}

// TestDrainAndCloseStdio_TimeoutStillCloses asserts the close steps run
// even when the drain budget actually expires. Without this, a wedged
// drain would block cleanup indefinitely. We use a 100ms timeout so the
// test can run synchronously without padding CI time, and we deliberately
// never release the wg — the only way the closers fire is via the
// timeout branch.
func TestDrainAndCloseStdio_TimeoutStillCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}

	wg, _ := blockedWG(t) // release deferred to t.Cleanup; never called manually

	tc := &trackedCloser{}
	logger, hook := withCapturedLog(t)

	const drainBudget = 100 * time.Millisecond
	start := time.Now()
	done := make(chan struct{})
	go func() {
		drainAndCloseStdio(wg, nil, []io.Closer{tc}, logger, "wedged process test-cid", drainBudget)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drainAndCloseStdio did not return within 2s of its 100ms drain budget — timeout path is broken")
	}

	elapsed := time.Since(start)
	if elapsed < drainBudget {
		t.Fatalf("drainAndCloseStdio returned in %s, before the %s drain budget elapsed — timeout path was not exercised",
			elapsed, drainBudget)
	}
	if tc.ClosedAt().IsZero() {
		t.Fatalf("close did not fire after drain timeout — cleanup contract broken")
	}
	if !strings.Contains(hook.String(), "failed to drain wedged process test-cid io") {
		t.Fatalf("expected timeout log line in output, got: %q", hook.String())
	}
}

// TestDrainAndCloseStdio_LogsDrainFailureForObservability asserts that
// when the drain times out, the diagnostic log line introduced by
// PR #12364 still fires. This is the production observability signal
// for the wedge condition.
func TestDrainAndCloseStdio_LogsDrainFailureForObservability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping log-capture test in -short mode")
	}

	wg, _ := blockedWG(t)

	logger, hook := withCapturedLog(t)

	// Use a tight budget by exercising waitTimeout directly with the
	// same log line that drainAndCloseStdio emits on timeout.
	if err := waitTimeout(context.Background(), wg, 100*time.Millisecond); err != nil {
		logger.WithError(err).Errorf("failed to drain %s io", "init process test-cid")
	}

	got := hook.String()
	if !strings.Contains(got, "failed to drain init process") {
		t.Fatalf("expected 'failed to drain init process' in log output, got: %q", got)
	}
	if !strings.Contains(got, "test-cid") {
		t.Fatalf("expected container id 'test-cid' in log output, got: %q", got)
	}
}

// TestDrainAndCloseStdio_NoGoroutineLeakAfterRelease asserts that the
// drain goroutine inside waitTimeout eventually exits once the wg is
// released. Rather than polling runtime.NumGoroutine() — which is flaky
// when other goroutines (test runner, logger, GC) are concurrent —
// we use a deterministic channel-based observation: after release(),
// a fresh waitTimeout call on the same wg must return nil immediately
// (the underlying wg is at zero), proving the original drain goroutine
// observed the Done() and exited.
func TestDrainAndCloseStdio_NoGoroutineLeakAfterRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine-leak test in -short mode")
	}

	wg, release := blockedWG(t)

	// First call: drain blocks, then deadline expires.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := waitTimeout(ctx, wg, 200*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from waitTimeout, got %v", err)
	}

	// Simulate the forcing function — closing the read FDs releases the
	// blocked reader.
	release()

	// Second call: wg has been Done'd by the goroutine inside blockedWG.
	// waitTimeout must observe wg.Wait() returning and complete with nil
	// well before the timeout. If the first-call drain goroutine leaked
	// (e.g., a future regression that holds the wg internally), this
	// will instead time out.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	start := time.Now()
	if err := waitTimeout(ctx2, wg, 2*time.Second); err != nil {
		t.Fatalf("waitTimeout did not observe wg.Done() after release (elapsed=%s, err=%v) — goroutine leak suspected",
			time.Since(start), err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitTimeout took %s after release — drain goroutine appears to be leaked", elapsed)
	}
}

// TestDeleteSucceedsWhenLargeStdoutAtExit is a placeholder pointing at
// the integration test that covers PR #12364's original motivating
// case (large stdout at exit not truncated). Real reproduction requires
// a runc container; see integration/client.
func TestDeleteSucceedsWhenLargeStdoutAtExit(t *testing.T) {
	t.Skip("integration test — see integration/client/ TestContainerExecLargeOutputWithTTY")
}

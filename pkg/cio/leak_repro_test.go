//go:build linux

/*
   Repro for containerd "thread exhaustion" leak.

   Opening the READ end of a container stdout/stderr FIFO blocks a background
   goroutine in open(2) (github.com/containerd/fifo.openFifo) until the task
   opens the WRITE end. That blocked syscall pins a dedicated OS thread. If the
   task never brings up its stdio (e.g. it never starts), the read-open is only
   released by Cancel()/Close(). Left unreleased and repeated under churn, the
   OS-thread count climbs to Go's 10,000-thread limit and containerd aborts with
   "fatal error: thread exhaustion".
*/

package cio

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func osThreads(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("read /proc/self/status: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Threads:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				t.Fatalf("parse Threads: %q: %v", v, err)
			}
			return n
		}
	}
	t.Fatal(`"Threads:" not found in /proc/self/status`)
	return 0
}

func countBlockedFifoOpens() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return strings.Count(string(buf), "fifo.openFifo")
}

func waitFor(cond func() bool, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestContainerLogFifoOpenLeaksThreadUntilClosed demonstrates (1) that each
// writer-less container stdout/stderr FIFO read-open pins an OS thread, and
// (2) that Close() releases them — i.e. the fix is ensuring Close()/Cancel()
// runs whenever the task's write end never opens.
func TestContainerLogFifoOpenLeaksThreadUntilClosed(t *testing.T) {
	dir := t.TempDir()
	const n = 300

	baseThreads := osThreads(t)
	baseFifo := countBlockedFifoOpens()

	dios := make([]*DirectIO, 0, n)
	for i := 0; i < n; i++ {
		// stdout+stderr only (no stdin -> avoid O_WRONLY/ENXIO on open).
		// No peer will ever open the write end: simulates a task that never starts.
		fs := NewFIFOSet(Config{
			Stdout: fmt.Sprintf("%s/c%d.stdout", dir, i),
			Stderr: fmt.Sprintf("%s/c%d.stderr", dir, i),
		}, func() error { return nil })
		dio, err := NewDirectIO(context.Background(), fs)
		if err != nil {
			t.Fatalf("NewDirectIO[%d]: %v", i, err)
		}
		dios = append(dios, dio)
	}

	// 2 fifos (stdout+stderr) per DirectIO; fifo.openFifo spawns 2 goroutines each.
	wantFifoG := 4 * n
	waitFor(func() bool { return countBlockedFifoOpens()-baseFifo >= wantFifoG }, 5*time.Second)

	leakThreads := osThreads(t)
	leakFifo := countBlockedFifoOpens()
	t.Logf("opened %d writer-less stdout+stderr FIFOs: OS threads %d -> %d (delta %d); blocked fifo.openFifo goroutines delta %d",
		n, baseThreads, leakThreads, leakThreads-baseThreads, leakFifo-baseFifo)

	if got := leakThreads - baseThreads; got < n/2 {
		t.Fatalf("expected OS-thread count to grow ~%d (one per blocked FIFO open); grew only %d", n, got)
	}

	// The fix lever: Close() unblocks the pending opens (fifo reverse-open),
	// the goroutines exit and the pinned threads become reusable.
	for _, dio := range dios {
		_ = dio.Close()
	}
	waitFor(func() bool { return countBlockedFifoOpens()-baseFifo <= 2 }, 15*time.Second)

	if got := countBlockedFifoOpens() - baseFifo; got > 2 {
		t.Fatalf("after Close(): expected blocked opens released; still %d above baseline (leak)", got)
	}
	t.Logf("after Close(): blocked fifo.openFifo goroutines back to baseline — leak released")
}

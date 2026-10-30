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
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"golang.org/x/sys/unix"
)

func TestNewBinaryIO(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	uri, _ := url.Parse("binary:///bin/echo?test")

	before := descriptorCount(t)

	io, err := NewBinaryIO(ctx, "1", uri)
	if err != nil {
		t.Fatal(err)
	}

	err = io.Close()
	if err != nil {
		t.Fatal(err)
	}

	after := descriptorCount(t)
	if before != after-1 { // one descriptor must be closed from shim logger side
		t.Fatalf("some descriptors weren't closed (%d != %d -1)", before, after)
	}
}

func TestNewBinaryIOBinaryV2Ready(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	// Use a shell command that writes a byte to fd 5 (CONTAINER_WAIT) to signal readiness
	uri, _ := url.Parse("binary-v2:///bin/sh?-c=printf%20x%20%3E%265")

	before := descriptorCount(t)

	io, err := NewBinaryIO(ctx, "2", uri)
	if err != nil {
		t.Fatal(err)
	}

	err = io.Close()
	if err != nil {
		t.Fatal(err)
	}

	after := descriptorCount(t)
	if before != after-1 { // one descriptor must be closed from shim logger side
		t.Fatalf("some descriptors weren't closed (%d != %d -1)", before, after)
	}
}

func TestNewBinaryIOBinaryV2FailsWhenLoggerDoesNotSignalReady(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	// Use /bin/true which exits without writing to fd 5 (CONTAINER_WAIT)
	uri, _ := url.Parse("binary-v2:///bin/true")

	before := descriptorCount(t)

	_, err := NewBinaryIO(ctx, "3", uri)
	if err == nil {
		t.Fatal("expected error when logger doesn't signal readiness")
	}
	if !strings.Contains(err.Error(), "did not call ready") {
		t.Fatalf("unexpected error: %v", err)
	}

	after := descriptorCount(t)
	if before != after {
		t.Fatalf("some descriptors weren't closed (%d != %d)", before, after)
	}
}

func TestNewBinaryIOCleanup(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	uri, _ := url.Parse("binary:///not/existing")

	before := descriptorCount(t)
	_, err := NewBinaryIO(ctx, "2", uri)
	if err == nil {
		t.Fatal("error expected for invalid binary")
	}

	after := descriptorCount(t)
	if before != after {
		t.Fatalf("some descriptors weren't closed (%d != %d)", before, after)
	}
}

// testCloser counts how many times Close is called on it.
type testCloser struct{ closed int }

func (c *testCloser) Close() error {
	c.closed++
	return nil
}

// TestProcessIOTrack documents the bookkeeping done by processIO.track:
// CloseReaders only releases the read ends, Close releases everything that is
// still registered, and a fifo end registered after the io was closed is
// closed right away instead of being leaked.
func TestProcessIOTrack(t *testing.T) {
	pio := &processIO{}

	w, r := &testCloser{}, &testCloser{}
	pio.addWriter(w)
	pio.addReader(r)

	pio.CloseReaders()
	if r.closed != 1 {
		t.Fatalf("read end closed %d times, want 1", r.closed)
	}
	if w.closed != 0 {
		t.Fatalf("write end closed %d times, want 0: writes must keep working while draining", w.closed)
	}

	if err := pio.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if w.closed != 1 {
		t.Fatalf("write end closed %d times, want 1", w.closed)
	}

	// A fifo end registered after the io was closed must not stay open, the
	// copy setup can race with the delete path.
	lateW, lateR := &testCloser{}, &testCloser{}
	pio.addWriter(lateW)
	pio.addReader(lateR)
	if lateW.closed != 1 || lateR.closed != 1 {
		t.Fatalf("fifo end registered after Close was leaked: write=%d read=%d", lateW.closed, lateR.closed)
	}
}

// testRuncIO is a minimal runc.IO implementation for copyPipes tests.
type testRuncIO struct {
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (t *testRuncIO) Close() error          { return nil }
func (t *testRuncIO) Stdin() io.WriteCloser { return nil }
func (t *testRuncIO) Stdout() io.ReadCloser { return t.stdout }
func (t *testRuncIO) Stderr() io.ReadCloser { return t.stderr }
func (t *testRuncIO) Set(*exec.Cmd)         {}

// newTestFIFOs creates the stdout and stderr fifos used by copyPipes.
func newTestFIFOs(t *testing.T) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	stdout = filepath.Join(dir, "stdout")
	stderr = filepath.Join(dir, "stderr")
	for _, p := range []string{stdout, stderr} {
		if err := syscall.Mkfifo(p, 0600); err != nil {
			t.Fatalf("failed to create fifo %s: %v", p, err)
		}
	}
	return stdout, stderr
}

// openFIFOConsumer opens the read side of a fifo the same way pkg/cio does.
func openFIFOConsumer(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("failed to open fifo %s for reading: %v", path, err)
	}
	return f
}

// readFIFO reads exactly n bytes from a non-blocking fifo read end.
func readFIFO(t *testing.T, f *os.File, n int) string {
	t.Helper()
	var (
		out      = make([]byte, 0, n)
		buf      = make([]byte, n)
		deadline = time.Now().Add(5 * time.Second)
	)
	for len(out) < n {
		nr, err := unix.Read(int(f.Fd()), buf)
		switch {
		case err == nil && nr > 0:
			out = append(out, buf[:nr]...)
		case err == nil:
			t.Fatalf("unexpected EOF after %d of %d bytes: %q", len(out), n, out)
		case errors.Is(err, unix.EAGAIN):
			if time.Now().After(deadline) {
				t.Fatalf("timed out reading from fifo, got %d of %d bytes: %q", len(out), n, out)
			}
			time.Sleep(10 * time.Millisecond)
		default:
			t.Fatalf("failed to read from fifo: %v", err)
		}
	}
	return string(out)
}

// fillFIFO writes to path until the fifo is full. The write end is opened in
// non-blocking mode so that the function can detect a full fifo instead of
// blocking forever.
func fillFIFO(t *testing.T, path string) {
	t.Helper()
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("failed to open fifo %s for filling: %v", path, err)
	}
	defer unix.Close(fd)

	buf := make([]byte, 4096)
	for {
		if _, err := unix.Write(fd, buf); err != nil {
			if err == unix.EAGAIN {
				return
			}
			t.Fatalf("failed to fill fifo %s: %v", path, err)
		}
	}
}

// waitGroupTimeout waits for wg with the given timeout.
func waitGroupTimeout(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("io copy goroutines did not finish in time")
	}
}

// TestCopyPipesFIFOReconnect reproduces a log consumer that detaches from the
// fifo and attaches again later, which is what happens when containerd is
// restarted or when `ctr task attach` exits.
//
// The shim keeps its own read end of the fifo open, so the fifo survives the
// disconnect: output produced while no consumer is attached stays buffered and
// is delivered to the next consumer, instead of the copy dying with EPIPE and
// leaving a permanently dead log stream.
func TestCopyPipesFIFOReconnect(t *testing.T) {
	stdout, stderr := newTestFIFOs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// copyPipes opens the fifos for writing, so both need a reader first.
	first := openFIFOConsumer(t, stdout)
	defer first.Close()
	stderrConsumer := openFIFOConsumer(t, stderr)
	defer stderrConsumer.Close()

	srcR, srcW := io.Pipe()
	rio := &testRuncIO{
		stdout: srcR,
		stderr: io.NopCloser(bytes.NewReader(nil)),
	}
	pio := &processIO{
		io:   rio,
		copy: true,
		stdio: stdio.Stdio{
			Stdout: stdout,
			Stderr: stderr,
		},
	}
	defer pio.Close()

	var wg sync.WaitGroup
	if err := pio.Copy(ctx, &wg); err != nil {
		t.Fatalf("copyPipes failed: %v", err)
	}

	if _, err := srcW.Write([]byte("hello")); err != nil {
		t.Fatalf("failed to write to the container stdout: %v", err)
	}
	if got := readFIFO(t, first, len("hello")); got != "hello" {
		t.Fatalf("consumer got %q, want %q", got, "hello")
	}

	// The consumer goes away, e.g. containerd is restarting.
	if err := first.Close(); err != nil {
		t.Fatalf("failed to close the first consumer: %v", err)
	}

	// Output produced while no consumer is attached must not fail.
	if _, err := srcW.Write([]byte("world")); err != nil {
		t.Fatalf("failed to write to the container stdout after the consumer went away: %v", err)
	}

	// A new consumer attaches to the same fifo and receives the output.
	second := openFIFOConsumer(t, stdout)
	defer second.Close()
	if got := readFIFO(t, second, len("world")); got != "world" {
		t.Fatalf("reconnected consumer got %q, want %q", got, "world")
	}

	// Closing the source lets the copy finish.
	srcW.Close()
	waitGroupTimeout(t, &wg, 5*time.Second)
}

// TestCopyPipesCloseReadersUnblocksCopy covers the delete path: once the consumer
// is gone and the fifo is full, the copy is blocked in write(2). Releasing the
// fifo read ends through processIO.CloseReaders must unblock it, otherwise
// Delete always has to wait for its 10 second io drain timeout.
func TestCopyPipesCloseReadersUnblocksCopy(t *testing.T) {
	stdout, stderr := newTestFIFOs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	consumer := openFIFOConsumer(t, stdout)
	defer consumer.Close()
	stderrConsumer := openFIFOConsumer(t, stderr)
	defer stderrConsumer.Close()

	// Fill the fifo so that the first write of the copy blocks.
	fillFIFO(t, stdout)

	rio := &testRuncIO{
		stdout: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 4096))),
		stderr: io.NopCloser(bytes.NewReader(nil)),
	}
	pio := &processIO{
		io:   rio,
		copy: true,
		stdio: stdio.Stdio{
			Stdout: stdout,
			Stderr: stderr,
		},
	}

	var wg sync.WaitGroup
	if err := pio.Copy(ctx, &wg); err != nil {
		t.Fatalf("copyPipes failed: %v", err)
	}

	if n := countFIFOFDs(t, stdout); n == 0 {
		t.Fatal("the copy did not open the stdout fifo")
	}

	// The consumers go away while the copy is blocked on the full fifo.
	if err := consumer.Close(); err != nil {
		t.Fatalf("failed to close the consumer: %v", err)
	}
	if err := stderrConsumer.Close(); err != nil {
		t.Fatalf("failed to close the stderr consumer: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the copy finished before the fifo ends were released; the test expects it to be blocked")
	case <-time.After(200 * time.Millisecond):
	}

	pio.CloseReaders()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the copy is still blocked after CloseReaders released the fifo ends")
	}

	// All fifo file descriptors held by the shim must be released, otherwise
	// deleting a process would leak them until the shim exits.
	requireNoFIFOFDs(t, stdout)
	requireNoFIFOFDs(t, stderr)
}

// requireNoFIFOFDs fails if a file descriptor that points at path is still open.
func requireNoFIFOFDs(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if countFIFOFDs(t, path) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("file descriptors of %s were leaked", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// countFIFOFDs returns the number of file descriptors that point at path.
func countFIFOFDs(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("failed to read /proc/self/fd: %v", err)
	}
	n := 0
	for _, e := range entries {
		if sym, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && sym == path {
			n++
		}
	}
	return n
}

// TestCopyPipesCloseReadersDoesNotTruncate makes sure that releasing the read
// ends when a process is deleted does not cut off output that is still being
// drained to an attached consumer.
func TestCopyPipesCloseReadersDoesNotTruncate(t *testing.T) {
	stdout, stderr := newTestFIFOs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	consumer := openFIFOConsumer(t, stdout)
	defer consumer.Close()
	stderrConsumer := openFIFOConsumer(t, stderr)
	defer stderrConsumer.Close()

	const size = 256 * 1024
	rio := &testRuncIO{
		stdout: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), size))),
		stderr: io.NopCloser(bytes.NewReader(nil)),
	}
	pio := &processIO{
		io:   rio,
		copy: true,
		stdio: stdio.Stdio{
			Stdout: stdout,
			Stderr: stderr,
		},
	}

	// The consumer keeps draining, slowly enough that the copy is still in
	// flight when CloseReaders is called. It is started after Copy so that the
	// copy already holds a write end of the fifo, otherwise the read would see
	// EOF until a writer shows up.
	fd := int(consumer.Fd())
	received := make(chan int, 1)
	drain := func() {
		var (
			n        int
			buf      = make([]byte, 4096)
			deadline = time.Now().Add(10 * time.Second)
		)
		for n < size {
			nr, err := unix.Read(fd, buf)
			switch {
			case err == nil && nr > 0:
				n += nr
			case err == nil:
				// No writer attached yet, wait for one.
				if n > 0 {
					received <- n
					return
				}
			case errors.Is(err, unix.EAGAIN):
				if time.Now().After(deadline) {
					received <- n
					return
				}
			default:
				received <- n
				return
			}
			time.Sleep(time.Millisecond)
		}
		received <- n
	}

	var wg sync.WaitGroup
	if err := pio.Copy(ctx, &wg); err != nil {
		t.Fatalf("copyPipes failed: %v", err)
	}

	go drain()

	pio.CloseReaders()
	waitGroupTimeout(t, &wg, 10*time.Second)

	select {
	case n := <-received:
		if n != size {
			t.Fatalf("consumer received %d of %d bytes", n, size)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the consumer")
	}
}

// TestCopyPipesCloseUnblocksStuckCopy makes sure that processIO.Close can
// release a copy that is stuck in write(2) even while the fifo still has a
// reader. This is why the write ends are tracked too: a full fifo with an
// attached but stalled consumer cannot be unblocked by closing the read ends,
// only by closing the write end that the copy is blocked on.
func TestCopyPipesCloseUnblocksStuckCopy(t *testing.T) {
	stdout, stderr := newTestFIFOs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The consumers stay attached for the whole test but never read, so the
	// copy cannot be unblocked by EPIPE.
	consumer := openFIFOConsumer(t, stdout)
	defer consumer.Close()
	stderrConsumer := openFIFOConsumer(t, stderr)
	defer stderrConsumer.Close()

	// Fill the fifo so that the first write of the copy blocks.
	fillFIFO(t, stdout)

	rio := &testRuncIO{
		stdout: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 4096))),
		stderr: io.NopCloser(bytes.NewReader(nil)),
	}
	pio := &processIO{
		io:   rio,
		copy: true,
		stdio: stdio.Stdio{
			Stdout: stdout,
			Stderr: stderr,
		},
	}

	var wg sync.WaitGroup
	if err := pio.Copy(ctx, &wg); err != nil {
		t.Fatalf("copyPipes failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the copy finished before the fifo ends were closed; the test expects it to be blocked")
	case <-time.After(200 * time.Millisecond):
	}

	pio.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the copy is still blocked after Close released the fifo write end")
	}

	// Close the test consumers first, countFIFOFDs would count them as well.
	if err := consumer.Close(); err != nil {
		t.Fatalf("failed to close the consumer: %v", err)
	}
	if err := stderrConsumer.Close(); err != nil {
		t.Fatalf("failed to close the stderr consumer: %v", err)
	}

	requireNoFIFOFDs(t, stdout)
	requireNoFIFOFDs(t, stderr)
}

func descriptorCount(t *testing.T) int {
	t.Helper()
	const dir = "/proc/self/fd"
	files, _ := os.ReadDir(dir)

	// Go 1.23+ uses pidfd instead of PID for processes started by a user,
	// if possible (see https://go.dev/cl/570036). As a side effect, every
	// os.StartProcess or os.FindProcess call results in an extra opened
	// file descriptor, which is only closed in p.Wait or p.Release.
	//
	// To retain compatibility with previous Go versions (or Go 1.23+
	// behavior on older kernels), let's not count pidfds.
	//
	// TODO: if the proposal to check for internal file descriptors
	// (https://go.dev/issues/67639) is accepted, we can use that
	// instead to detect internal fds in use by the Go runtime.
	count := 0
	for _, file := range files {
		sym, err := os.Readlink(filepath.Join(dir, file.Name()))
		// Either pidfd:[70517] or anon_inode:[pidfd] (on Linux 5.4).
		if err == nil && strings.Contains(sym, "pidfd") {
			continue
		}
		count++
	}

	return count
}

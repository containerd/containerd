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

package runc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/fifo"
)

type closeCountingConsole struct {
	console.Console
	closed int
}

func (c *closeCountingConsole) Close() error {
	c.closed++
	return c.Console.Close()
}

// A setup error inside CopyConsole must release the console master it
// already registered with the epoller.
func TestCopyConsoleReleasesConsoleOnSetupError(t *testing.T) {
	p, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	master, _, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	cons := &closeCountingConsole{Console: master}
	var wg sync.WaitGroup
	if _, err := p.CopyConsole(context.Background(), cons, "id", "", ":not-a-uri", "", &wg); err == nil {
		t.Fatal("CopyConsole accepted an unparsable stdout uri")
	}
	if cons.closed != 1 {
		t.Fatalf("console master closed %d times after a setup error, want 1", cons.closed)
	}
}

// A FIFO path with a reader goroutine the test drains, as the daemon would.
func drainedFifo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		r, err := fifo.OpenFifo(context.Background(), path, syscall.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer r.Close()
		io.Copy(io.Discard, r)
	}()
	return path
}

// The shim's master arrives over the runc console socket as a plain
// blocking file, outside Go's poller; this builds the same kind from a pty.
func receivedMaster(t *testing.T) console.Console {
	t.Helper()
	pty, slave, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pty.Close() })
	// Keep a slave open so reads on the master wait for data instead of
	// failing with EIO, as they do for a running process.
	s, err := os.OpenFile(slave, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fd, err := syscall.Dup(int(pty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	cons, err := console.ConsoleFromFile(os.NewFile(uintptr(fd), "ptmx"))
	if err != nil {
		t.Fatal(err)
	}
	return cons
}

// Closing the console CopyConsole returned shuts it down and deregisters it,
// so the stdout copier parked on the epoll condition wakes and finishes; a
// bare EpollConsole.Close would leave it asleep. Once released, a late
// ShutdownConsole is a no-op: it must not touch the epoller again.
func TestConsoleCloseEndsTheCopier(t *testing.T) {
	p, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	master := receivedMaster(t)
	var wg sync.WaitGroup
	cons, err := p.CopyConsole(context.Background(), master, "id", "", drainedFifo(t), "", &wg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	if err := cons.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stdout copier stayed asleep after Close")
	}
	if err := cons.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	if err := p.ShutdownConsole(context.Background(), cons); err != nil {
		t.Fatalf("ShutdownConsole after Close = %v, want nil: the released console must not be deregistered again", err)
	}
}

func fdsReferring(t *testing.T, path string) int {
	t.Helper()
	var want syscall.Stat_t
	if err := syscall.Stat(path, &want); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		var st syscall.Stat_t
		if err := syscall.Stat(filepath.Join("/proc/self/fd", e.Name()), &st); err != nil {
			continue // closed between ReadDir and Stat
		}
		if st.Dev == want.Dev && st.Ino == want.Ino {
			n++
		}
	}
	return n
}

// A setup error after the stdin copier started must release the stdin FIFO
// reader (and the fifo package's O_PATH handle behind it), or the copier
// stays blocked on it for as long as the caller's own writer lives.
func TestCopyConsoleSetupErrorReleasesStdinFifo(t *testing.T) {
	p, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	master, _, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	stdin := filepath.Join(t.TempDir(), "stdin")
	if err := syscall.Mkfifo(stdin, 0o600); err != nil {
		t.Fatal(err)
	}
	// Hold a writer the way the shim holds e.stdin, fully open before
	// CopyConsole runs: a throwaway non-blocking reader lets the writer's
	// open(2) complete now rather than in a goroutine.
	r, err := os.OpenFile(stdin, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(stdin, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	r.Close()
	before := fdsReferring(t, stdin)
	var wg sync.WaitGroup
	if _, err := p.CopyConsole(context.Background(), master, "id", stdin, ":not-a-uri", "", &wg); err == nil {
		t.Fatal("CopyConsole accepted an unparsable stdout uri")
	}
	if after := fdsReferring(t, stdin); after != before {
		t.Fatalf("descriptors on the stdin FIFO went %d -> %d across the failed setup: CopyConsole leaked its reader", before, after)
	}
}

// blockingMaster re-wraps a pty master as a blocking file outside Go's
// poller, the kind the shim receives over the runc console socket, and
// returns the fd number it occupies.
func blockingMaster(t *testing.T, pty console.Console) (console.Console, int) {
	t.Helper()
	fd, err := syscall.Dup(int(pty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	cons, err := console.ConsoleFromFile(os.NewFile(uintptr(fd), "ptmx"))
	if err != nil {
		t.Fatal(err)
	}
	return cons, fd
}

// A console released by Close frees its fd number; the next console in the
// shim takes it. A late ShutdownConsole for the old one, as setExited or
// delete issue, must not deregister the new one.
func TestShutdownAfterCloseLeavesAReusedFdAlone(t *testing.T) {
	p, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	ctx := context.Background()
	ptyA, slaveA, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptyA.Close() })
	sA, err := os.OpenFile(slaveA, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sA.Close() })
	masterA, fdA := blockingMaster(t, ptyA)
	var wgA sync.WaitGroup
	consA, err := p.CopyConsole(ctx, masterA, "a", "", drainedFifo(t), "", &wgA)
	if err != nil {
		t.Fatal(err)
	}
	ptyB, slaveB, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptyB.Close() })
	sB, err := os.OpenFile(slaveB, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sB.Close() })
	if err := consA.Close(); err != nil {
		t.Fatal(err)
	}
	masterB, fdB := blockingMaster(t, ptyB)
	if fdB != fdA {
		t.Skipf("the freed fd %d was not reused (got %d); the scenario needs the reuse", fdA, fdB)
	}
	out := filepath.Join(t.TempDir(), "stdout")
	if err := syscall.Mkfifo(out, 0o600); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		r, err := fifo.OpenFifo(ctx, out, syscall.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer r.Close()
		buf := make([]byte, 64)
		n, _ := r.Read(buf)
		got <- string(buf[:n])
	}()
	var wgB sync.WaitGroup
	consB, err := p.CopyConsole(ctx, masterB, "b", "", out, "", &wgB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { consB.Close() })
	// the late shutdown of the released console
	if err := p.ShutdownConsole(ctx, consA); err != nil {
		t.Fatal(err)
	}
	if _, err := sB.Write([]byte("still here\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		// the pty's output translation turns the newline into CR LF
		if s != "still here\r\n" {
			t.Fatalf("B delivered %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B's copier delivered nothing: the stale shutdown deregistered the reused fd")
	}
}

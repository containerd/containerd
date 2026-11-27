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
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
)

func TestProcessIOStdinClose(t *testing.T) {
	for _, tc := range []struct {
		name            string
		input           string
		attach          bool
		closeBeforeCopy bool
		cancelSetup     bool
	}{
		{name: "no-client"},
		{name: "empty-input", attach: true},
		{name: "empty-client-before-copy", closeBeforeCopy: true},
		{name: "input", input: "stdin before close", attach: true},
		{name: "input-after-setup-cancel", input: "stdin after setup cancellation", attach: true, cancelSetup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pio, processStdin := newStdinProcessIO(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			p := &Init{}
			if err := p.openStdin(pio.stdio.Stdin); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { p.Stdin().Close() })
			if tc.closeBeforeCopy {
				writer, err := fifo.OpenFifo(ctx, pio.stdio.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			setupCtx, cancelSetup := context.WithCancel(ctx)
			defer cancelSetup()
			var wg sync.WaitGroup
			if err := pio.Copy(setupCtx, &wg); err != nil {
				t.Fatal(err)
			}
			if tc.cancelSetup {
				cancelSetup()
			}
			var writer io.WriteCloser
			if tc.attach {
				var err error
				writer, err = fifo.OpenFifo(ctx, pio.stdio.Stdin, syscall.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { writer.Close() })
			}
			if err := p.Stdin().Close(); err != nil {
				t.Fatal(err)
			}
			sent := make(chan error, 1)
			go func() {
				var err error
				if writer != nil {
					_, err = io.WriteString(writer, tc.input)
					writer.Close()
				}
				sent <- err
			}()
			type result struct {
				data []byte
				err  error
			}
			received := make(chan result, 1)
			go func() {
				data, err := io.ReadAll(processStdin)
				received <- result{data, err}
			}()
			select {
			case res := <-received:
				if res.err != nil {
					t.Fatal(res.err)
				}
				if string(res.data) != tc.input {
					t.Fatalf("stdin = %q, want %q", res.data, tc.input)
				}
			case <-ctx.Done():
				t.Fatal("stdin did not reach EOF after closing both writers")
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
			wg.Wait()
		})
	}
}

func TestProcessIOStdinOpenCanceled(t *testing.T) {
	for _, phase := range []string{"before-open", "during-open"} {
		t.Run(phase, func(t *testing.T) {
			pio, _ := newStdinProcessIO(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			openCtx, cancelOpen := context.WithCancel(ctx)
			defer cancelOpen()
			observed := &stdinOpenContext{
				Context:    openCtx,
				doneCalled: make(chan struct{}),
			}
			if phase == "before-open" {
				cancelOpen()
			}
			var wg sync.WaitGroup
			copied := make(chan error, 1)
			go func() { copied <- pio.Copy(observed, &wg) }()
			if phase == "during-open" {
				select {
				case <-observed.doneCalled:
					cancelOpen()
				case <-ctx.Done():
					t.Fatal("stdin open did not observe the setup context")
				}
			}
			select {
			case err := <-copied:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("opening stdin without a writer = %v, want context.Canceled", err)
				}
			case <-ctx.Done():
				t.Fatal("stdin open did not return after cancellation")
			}
			wg.Wait()
		})
	}
}

type stdinOpenContext struct {
	context.Context
	doneCalled chan struct{}
	once       sync.Once
}

func (c *stdinOpenContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.doneCalled) })
	return c.Context.Done()
}

func newStdinProcessIO(t *testing.T) (*processIO, io.Reader) {
	t.Helper()
	dir := t.TempDir()
	// FIFO cancellation and pipe copying release descriptors asynchronously.
	// Wait after closing the I/O, but before removing its files.
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			fds, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			var pending []string
			for _, fd := range fds {
				target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if strings.HasPrefix(target, dir+string(os.PathSeparator)) {
					pending = append(pending, target)
				}
			}
			if len(pending) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("I/O descriptors were not released: %v", pending)
			}
			time.Sleep(time.Millisecond)
		}
	})
	stdin := filepath.Join(dir, "stdin")
	if err := syscall.Mkfifo(stdin, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	if err := os.WriteFile(output, nil, 0600); err != nil {
		t.Fatal(err)
	}
	pio, err := createIO(t.Context(), t.Name(), os.Getuid(), os.Getgid(), stdio.Stdio{
		Stdin: stdin, Stdout: output, Stderr: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pio.Close() })
	cmd := &exec.Cmd{}
	pio.IO().Set(cmd)
	if err := pio.IO().(runc.StartCloser).CloseAfterStart(); err != nil {
		t.Fatal(err)
	}
	return pio, cmd.Stdin
}

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

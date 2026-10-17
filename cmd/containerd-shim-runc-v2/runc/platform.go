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

package runc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"
	"syscall"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/fifo"
)

var bufPool = sync.Pool{
	New: func() any {
		// setting to 4096 to align with PIPE_BUF
		// http://man7.org/linux/man-pages/man7/pipe.7.html
		buffer := make([]byte, 4096)
		return &buffer
	},
}

// NewPlatform returns a linux platform for use with I/O operations
func NewPlatform() (stdio.Platform, error) {
	epoller, err := console.NewEpoller()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize epoller: %w", err)
	}
	go epoller.Wait()
	return &linuxPlatform{
		epoller: epoller,
	}, nil
}

type linuxPlatform struct {
	epoller *console.Epoller
}

func (p *linuxPlatform) CopyConsole(ctx context.Context, console console.Console, id, stdin, stdout, stderr string, wg *sync.WaitGroup) (cons console.Console, retErr error) {
	if p.epoller == nil {
		return nil, errors.New("uninitialized epoller")
	}

	epollConsole, err := p.epoller.Add(console)
	if err != nil {
		console.Close()
		return nil, err
	}
	cc := &closingConsole{EpollConsole: epollConsole, epoller: p.epoller}
	defer func() {
		if retErr != nil {
			cc.Close()
		}
	}()

	var cwg sync.WaitGroup
	if stdin != "" {
		in, err := fifo.OpenFifo(context.Background(), stdin, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		cc.stdin = in
		cwg.Add(1)
		go func() {
			cwg.Done()
			bp := bufPool.Get().(*[]byte)
			defer bufPool.Put(bp)
			io.CopyBuffer(epollConsole, in, *bp)
			// stdin closed or broken: release the console
			cc.Close()
		}()
	}

	uri, err := url.Parse(stdout)
	if err != nil {
		return nil, fmt.Errorf("unable to parse stdout uri: %w", err)
	}

	switch uri.Scheme {
	case "binary", "binary-v2":
		ns, err := namespaces.NamespaceRequired(ctx)
		if err != nil {
			return nil, err
		}
		strictReady := uri.Scheme == "binary-v2"

		cmd := process.NewBinaryCmd(uri, id, ns)

		// In case of unexpected errors during logging binary start, close open pipes
		var filesToClose []*os.File

		defer func() {
			if retErr != nil {
				process.CloseFiles(filesToClose...)
			}
		}()

		// Create pipe to be used by logging binary for Stdout
		outR, outW, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipes: %w", err)
		}
		filesToClose = append(filesToClose, outR)

		// Stderr is created for logging binary but unused when terminal is true
		serrR, _, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stderr pipes: %w", err)
		}
		filesToClose = append(filesToClose, serrR)

		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		filesToClose = append(filesToClose, r)

		cmd.ExtraFiles = append(cmd.ExtraFiles, outR, serrR, w)

		wg.Add(1)
		cwg.Add(1)
		go func() {
			cwg.Done()
			io.Copy(outW, epollConsole)
			outW.Close()
			wg.Done()
		}()

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start logging binary process: %w", err)
		}

		// Close our side of the pipe after start
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("failed to close write pipe after start: %w", err)
		}

		// Wait for the logging binary to be ready
		// For binary-v2, readiness requires a byte to be written before close.
		// For binary, EOF is treated as ready for backward compatibility.
		b := make([]byte, 1)
		n, err := r.Read(b)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read from logging binary: %w", err)
		}
		if strictReady && n == 0 {
			return nil, errors.New("logging binary did not call ready (it may have crashed or exited prematurely)")
		}
		cwg.Wait()

	default:
		outw, err := fifo.OpenFifo(ctx, stdout, syscall.O_WRONLY, 0)
		if err != nil {
			return nil, err
		}
		outr, err := fifo.OpenFifo(ctx, stdout, syscall.O_RDONLY, 0)
		if err != nil {
			outw.Close()
			return nil, err
		}
		wg.Add(1)
		cwg.Add(1)
		go func() {
			cwg.Done()
			buf := bufPool.Get().(*[]byte)
			defer bufPool.Put(buf)
			io.CopyBuffer(outw, epollConsole, *buf)

			outw.Close()
			outr.Close()
			wg.Done()
		}()
		cwg.Wait()
	}

	return cc, nil
}

func (p *linuxPlatform) ShutdownConsole(ctx context.Context, cons console.Console) error {
	if p.epoller == nil {
		return errors.New("uninitialized epoller")
	}
	cc, ok := cons.(*closingConsole)
	if !ok {
		return fmt.Errorf("expected closingConsole, got %#v", cons)
	}
	return cc.shutdown()
}

// closingConsole is what CopyConsole returns: the epoll console plus the stdin
// FIFO reader, so the teardown lives in one place.
//
// shutdown removes the console from the epoller, once, and only while the
// fd is still open. After the fd is closed its number can belong to another
// console, and removing it again would hit that one.
//
// Close runs shutdown, closes the master, and closes the stdin FIFO reader,
// once. Closing the reader is what stops the stdin copier, which is blocked
// reading it. The copier, CopyConsole's error path, ShutdownConsole and
// delete all release through these two.
type closingConsole struct {
	*console.EpollConsole
	epoller      *console.Epoller
	stdin        io.Closer
	shutdownOnce sync.Once
	shutdownErr  error
	closeOnce    sync.Once
	closeErr     error
}

func (t *closingConsole) shutdown() error {
	t.shutdownOnce.Do(func() {
		t.shutdownErr = t.Shutdown(t.epoller.CloseConsole)
	})
	return t.shutdownErr
}

func (t *closingConsole) Close() error {
	t.closeOnce.Do(func() {
		t.shutdown()
		t.closeErr = t.EpollConsole.Close()
		// a writer parked on the master before it closed retries on this
		// wake and sees the closed file instead of another hangup
		t.Shutdown(func(int) error { return nil })
		if t.stdin != nil {
			if err := t.stdin.Close(); err != nil && t.closeErr == nil {
				t.closeErr = err
			}
		}
	})
	return t.closeErr
}

func (p *linuxPlatform) Close() error {
	return p.epoller.Close()
}

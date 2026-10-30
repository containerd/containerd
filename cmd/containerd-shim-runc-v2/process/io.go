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

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/log"
)

const binaryIOProcTermTimeout = 12 * time.Second // Give logger process solid 10 seconds for cleanup

var bufPool = sync.Pool{
	New: func() any {
		// setting to 4096 to align with PIPE_BUF
		// http://man7.org/linux/man-pages/man7/pipe.7.html
		buffer := make([]byte, 4096)
		return &buffer
	},
}

type processIO struct {
	io runc.IO

	uri   *url.URL
	copy  bool
	stdio stdio.Stdio

	// mu protects fifos, readers and closing.
	mu    sync.Mutex
	fifos []io.Closer // fifo ends opened by copyPipes
	// readers is the subset of fifos that are read ends, they are released as
	// soon as CloseReaders is called.
	readers []io.Closer
	closing bool
}

// Close closes the fifo ends opened for copying and the underlying process io.
func (p *processIO) Close() error {
	p.mu.Lock()
	p.closing = true
	fifos := p.fifos
	p.fifos = nil
	p.readers = nil
	p.mu.Unlock()

	for _, f := range fifos {
		f.Close()
	}
	if p.io != nil {
		return p.io.Close()
	}
	return nil
}

// CloseReaders releases the read ends that copyPipes opened for the
// container's stdout, stderr and stdin.
//
// The shim keeps an extra read end of each fifo open so that a consumer which
// detaches and reconnects to the same fifo (containerd restart, `ctr task
// attach` exiting, ...) keeps receiving the output. That read end has to be
// released when the process goes away: as long as it is open, a write to a
// fifo whose real consumer is gone blocks forever instead of failing with
// EPIPE, so the copy could only be cleaned up by giving up on it after the
// Delete timeout.
//
// Only read ends are released here. Output that is still being drained is not
// affected, because writes keep succeeding while the real consumer is
// attached, and the write ends are closed by the copy goroutines themselves
// once they are done.
//
// The read ends are also still tracked in p.fifos, so Close may close them a
// second time. That is safe: fifo.Fifo.Close is idempotent.
func (p *processIO) CloseReaders() {
	p.mu.Lock()
	readers := p.readers
	p.readers = nil
	p.mu.Unlock()

	for _, r := range readers {
		r.Close()
	}
}

// addWriter registers a fifo write end so that it is released by Close.
func (p *processIO) addWriter(f io.Closer) {
	p.track(f, false)
}

// addReader registers a fifo read end so that it is released by Close and by
// CloseReaders.
func (p *processIO) addReader(f io.Closer) {
	p.track(f, true)
}

// track registers a fifo end to be closed with the process io. If the io is
// already closing, the fifo end is closed immediately instead of being leaked.
func (p *processIO) track(f io.Closer, reader bool) {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		f.Close()
		return
	}
	p.fifos = append(p.fifos, f)
	if reader {
		p.readers = append(p.readers, f)
	}
	p.mu.Unlock()
}

func (p *processIO) IO() runc.IO {
	return p.io
}

func (p *processIO) Copy(ctx context.Context, wg *sync.WaitGroup) error {
	if !p.copy {
		return nil
	}
	var cwg sync.WaitGroup
	if err := copyPipes(ctx, p, p.stdio.Stdin, p.stdio.Stdout, p.stdio.Stderr, wg, &cwg); err != nil {
		return fmt.Errorf("unable to copy pipes: %w", err)
	}
	cwg.Wait()
	return nil
}

func createIO(ctx context.Context, id string, ioUID, ioGID int, stdio stdio.Stdio) (*processIO, error) {
	pio := &processIO{
		stdio: stdio,
	}
	if stdio.IsNull() {
		i, err := runc.NewNullIO()
		if err != nil {
			return nil, err
		}
		pio.io = i
		return pio, nil
	}
	u, err := url.Parse(stdio.Stdout)
	if err != nil {
		return nil, fmt.Errorf("unable to parse stdout uri: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "fifo"
	}
	pio.uri = u
	switch u.Scheme {
	case "fifo":
		pio.copy = true
		pio.io, err = runc.NewPipeIO(ioUID, ioGID, withConditionalIO(stdio))
	case "binary", "binary-v2":
		pio.io, err = NewBinaryIO(ctx, id, u)
	case "file":
		filePath := u.Path
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, err
		}
		var f *os.File
		f, err = os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		f.Close()
		pio.stdio.Stdout = filePath
		pio.stdio.Stderr = filePath
		pio.copy = true
		pio.io, err = runc.NewPipeIO(ioUID, ioGID, withConditionalIO(stdio))
	default:
		return nil, fmt.Errorf("unknown STDIO scheme %s", u.Scheme)
	}
	if err != nil {
		return nil, err
	}
	return pio, nil
}

func copyPipes(ctx context.Context, pio *processIO, stdin, stdout, stderr string, wg, cwg *sync.WaitGroup) error {
	rio := pio.IO()
	var sameFile *countingWriteCloser
	for _, i := range []struct {
		name string
		dest func(wc io.WriteCloser, rc io.Closer)
	}{
		{
			name: stdout,
			dest: func(wc io.WriteCloser, rc io.Closer) {
				wg.Add(1)
				cwg.Add(1)
				go func() {
					cwg.Done()
					p := bufPool.Get().(*[]byte)
					defer bufPool.Put(p)
					if _, err := io.CopyBuffer(wc, rio.Stdout(), *p); err != nil {
						log.G(ctx).Warn("error copying stdout")
					}
					wg.Done()
					wc.Close()
					if rc != nil {
						rc.Close()
					}
				}()
			},
		}, {
			name: stderr,
			dest: func(wc io.WriteCloser, rc io.Closer) {
				wg.Add(1)
				cwg.Add(1)
				go func() {
					cwg.Done()
					p := bufPool.Get().(*[]byte)
					defer bufPool.Put(p)
					if _, err := io.CopyBuffer(wc, rio.Stderr(), *p); err != nil {
						log.G(ctx).Warn("error copying stderr")
					}
					wg.Done()
					wc.Close()
					if rc != nil {
						rc.Close()
					}
				}()
			},
		},
	} {
		ok, err := fifo.IsFifo(i.name)
		if err != nil {
			return err
		}
		var (
			fw io.WriteCloser
			fr io.Closer
		)
		if ok {
			if fw, err = fifo.OpenFifo(ctx, i.name, syscall.O_WRONLY, 0); err != nil {
				return fmt.Errorf("containerd-shim: opening w/o fifo %q failed: %w", i.name, err)
			}
			// Hold an extra read end open for as long as the process exists.
			// It keeps the fifo pipe object alive when the consumer goes away
			// (containerd restarting, `ctr task attach` detaching, ...), so a
			// write blocks instead of failing with EPIPE and a consumer that
			// reconnects to the same fifo still receives the output.
			// processIO.CloseReaders releases it when the process is deleted,
			// which unblocks the copy and lets it exit.
			if fr, err = fifo.OpenFifo(ctx, i.name, syscall.O_RDONLY, 0); err != nil {
				fw.Close()
				return fmt.Errorf("containerd-shim: opening r/o fifo %q failed: %w", i.name, err)
			}
			pio.addWriter(fw)
			pio.addReader(fr)
		} else {
			if sameFile != nil {
				sameFile.bumpCount(1)
				i.dest(sameFile, nil)
				continue
			}
			if fw, err = os.OpenFile(i.name, syscall.O_WRONLY|syscall.O_APPEND, 0); err != nil {
				return fmt.Errorf("containerd-shim: opening file %q failed: %w", i.name, err)
			}
			if stdout == stderr {
				sameFile = newCountingWriteCloser(fw, 1)
			}
		}
		i.dest(fw, fr)
	}
	if stdin == "" {
		return nil
	}
	f, err := fifo.OpenFifo(context.Background(), stdin, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("containerd-shim: opening %s failed: %s", stdin, err)
	}
	// Release the read end when the process is deleted so a blocked copy
	// does not keep the shim alive.
	pio.addReader(f)
	cwg.Add(1)
	go func() {
		cwg.Done()
		p := bufPool.Get().(*[]byte)
		defer bufPool.Put(p)

		io.CopyBuffer(rio.Stdin(), f, *p)
		rio.Stdin().Close()
		f.Close()
	}()
	return nil
}

// countingWriteCloser masks io.Closer() until close has been invoked a certain number of times.
type countingWriteCloser struct {
	io.WriteCloser
	count atomic.Int64
}

func newCountingWriteCloser(c io.WriteCloser, count int64) *countingWriteCloser {
	cwc := &countingWriteCloser{
		c,
		atomic.Int64{},
	}
	cwc.bumpCount(count)
	return cwc
}

func (c *countingWriteCloser) bumpCount(delta int64) int64 {
	return c.count.Add(delta)
}

func (c *countingWriteCloser) Close() error {
	if c.bumpCount(-1) > 0 {
		return nil
	}
	return c.WriteCloser.Close()
}

// NewBinaryIO runs a custom binary process for pluggable shim logging
func NewBinaryIO(ctx context.Context, id string, uri *url.URL) (_ runc.IO, err error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var closers []func() error
	defer func() {
		if err == nil {
			return
		}
		result := []error{err}
		for _, fn := range closers {
			result = append(result, fn())
		}
		err = errors.Join(result...)
	}()

	out, err := newPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipes: %w", err)
	}
	closers = append(closers, out.Close)

	serr, err := newPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipes: %w", err)
	}
	closers = append(closers, serr.Close)

	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	closers = append(closers, r.Close, w.Close)

	cmd := NewBinaryCmd(uri, id, ns)
	cmd.ExtraFiles = append(cmd.ExtraFiles, out.r, serr.r, w)
	// don't need to register this with the reaper or wait when
	// running inside a shim
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start binary process: %w", err)
	}
	closers = append(closers, func() error { return cmd.Process.Kill() })

	// close our side of the pipe after start
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to close write pipe after start: %w", err)
	}

	// wait for the logging binary to be ready
	// For binary-v2, readiness requires a byte to be written before close.
	// For binary, EOF is treated as ready for backward compatibility.
	b := make([]byte, 1)
	n, err := r.Read(b)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read from logging binary: %w", err)
	}
	if uri.Scheme == "binary-v2" && n == 0 {
		return nil, errors.New("logging binary did not call ready (it may have crashed or exited prematurely)")
	}

	return &binaryIO{
		cmd: cmd,
		out: out,
		err: serr,
	}, nil
}

type binaryIO struct {
	cmd      *exec.Cmd
	out, err *pipe
}

func (b *binaryIO) CloseAfterStart() error {
	var result []error

	for _, v := range []*pipe{b.out, b.err} {
		if v != nil {
			if err := v.r.Close(); err != nil {
				result = append(result, err)
			}
		}
	}

	return errors.Join(result...)
}

func (b *binaryIO) Close() error {
	var result []error

	for _, v := range []*pipe{b.out, b.err} {
		if v != nil {
			if err := v.Close(); err != nil {
				result = append(result, err)
			}
		}
	}

	if err := b.cancel(); err != nil {
		result = append(result, err)
	}

	return errors.Join(result...)
}

func (b *binaryIO) cancel() error {
	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}

	// Send SIGTERM first, so logger process has a chance to flush and exit properly
	if err := b.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		result := []error{fmt.Errorf("failed to send SIGTERM: %w", err)}

		log.L.WithError(err).Warn("failed to send SIGTERM signal, killing logging shim")

		if err := b.cmd.Process.Kill(); err != nil {
			result = append(result, fmt.Errorf("failed to kill process after faulty SIGTERM: %w", err))
		}

		return errors.Join(result...)
	}

	done := make(chan error, 1)
	go func() {
		done <- b.cmd.Wait()
	}()

	select {
	case err := <-done:
		// If the process exited due to the SIGTERM we just sent,
		// that is expected and should not be treated as an error.
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signal() == syscall.SIGTERM {
				return nil
			}
		}
		return err
	case <-time.After(binaryIOProcTermTimeout):
		log.L.Warn("failed to wait for shim logger process to exit, killing")

		err := b.cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("failed to kill shim logger process: %w", err)
		}

		return nil
	}
}

func (b *binaryIO) Stdin() io.WriteCloser {
	return nil
}

func (b *binaryIO) Stdout() io.ReadCloser {
	return nil
}

func (b *binaryIO) Stderr() io.ReadCloser {
	return nil
}

func (b *binaryIO) Set(cmd *exec.Cmd) {
	if b.out != nil {
		cmd.Stdout = b.out.w
	}
	if b.err != nil {
		cmd.Stderr = b.err.w
	}
}

func newPipe() (*pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &pipe{
		r: r,
		w: w,
	}, nil
}

type pipe struct {
	r *os.File
	w *os.File
}

func (p *pipe) Close() error {
	var result []error

	if err := p.w.Close(); err != nil {
		result = append(result, fmt.Errorf("pipe: failed to close write pipe: %w", err))
	}

	if err := p.r.Close(); err != nil {
		result = append(result, fmt.Errorf("pipe: failed to close read pipe: %w", err))
	}

	return errors.Join(result...)
}

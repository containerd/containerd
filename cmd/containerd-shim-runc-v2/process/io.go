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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	file  *File
}

func (p *processIO) Close() error {
	if p.io != nil {
		return p.io.Close()
	}
	return nil
}

func (p *processIO) IO() runc.IO {
	return p.io
}

func (p *processIO) File() *File {
	return p.file
}

func (p *processIO) Copy(ctx context.Context, wg *sync.WaitGroup) error {
	if !p.copy {
		return nil
	}
	var cwg sync.WaitGroup
	if err := p.copyPipes(ctx, p.IO(), p.stdio.Stdin, p.stdio.Stdout, p.stdio.Stderr, wg, &cwg); err != nil {
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
	case "binary":
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

func (p *processIO) copyPipes(ctx context.Context, rio runc.IO, stdin, stdout, stderr string, wg, cwg *sync.WaitGroup) error {
	var rootDir string
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
					if p.uri.Scheme != "file" {
						p := bufPool.Get().(*[]byte)
						defer bufPool.Put(p)
						if _, err := io.CopyBuffer(wc, rio.Stdout(), *p); err != nil {
							log.G(ctx).Warn("error copying stdout")
						}
					} else {
						u, err := url.Parse(p.uri.String())
						if err != nil {
							log.G(ctx).Warnf("error parse uri %v", err)
						}
						values, err := url.ParseQuery(u.RawQuery)
						if err != nil {
							log.G(ctx).Warnf("error parse query uri %v", err)
						}
						LogType := values.Get("log_type")
						rootDir = values.Get("container_root_dir")
						rbuf := bufio.NewReader(rio.Stdout())
						if LogType == "file" {
							// Format container logs to CRI format
							maxLineStr := values.Get("max_container_log_line_size")
							maxLine, err := strconv.Atoi(maxLineStr)
							if err != nil {
								log.G(ctx).Warnf("error trans %s to int %v", maxLineStr, err)
							}
							if p.uri.Path == "/dev/null" {
								log.G(ctx).Warnf("log path is empty")
								io.Copy(io.Discard, rio.Stdout())
							} else {
								redirectLogs(p.uri.Path, rbuf, wc, "stdout", maxLine)
							}
						}
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
					if p.uri.Scheme != "file" {
						p := bufPool.Get().(*[]byte)
						defer bufPool.Put(p)
						if _, err := io.CopyBuffer(wc, rio.Stderr(), *p); err != nil {
							log.G(ctx).Warn("error copying stderr")
						}
					} else {
						u, err := url.Parse(p.uri.String())
						if err != nil {
							log.G(ctx).Warnf("error parse uri %v", err)
						}
						values, err := url.ParseQuery(u.RawQuery)
						if err != nil {
							log.G(ctx).Warnf("error parse query uri %v", err)
						}
						LogType := values.Get("log_type")
						rootDir = values.Get("container_root_dir")
						rbuf := bufio.NewReader(rio.Stdout())
						if LogType == "file" {
							// Format container logs to CRI format
							maxLineStr := values.Get("max_container_log_line_size")
							maxLine, err := strconv.Atoi(maxLineStr)
							if err != nil {
								log.G(ctx).Warnf("error trans %s to int %v", maxLineStr, err)
							}
							if p.uri.Path == "/dev/null" {
								log.G(ctx).Warnf("log path is empty")
								io.Copy(io.Discard, rio.Stderr())
							} else {
								redirectLogs(p.uri.Path, rbuf, wc, "stdout", maxLine)
							}
						}
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
			if fr, err = fifo.OpenFifo(ctx, i.name, syscall.O_RDONLY, 0); err != nil {
				return fmt.Errorf("containerd-shim: opening r/o fifo %q failed: %w", i.name, err)
			}
		} else {
			if sameFile != nil {
				sameFile.bumpCount(1)
				i.dest(sameFile, nil)
				continue
			}
			file, err := OpenFile(i.name, rootDir)
			if err != nil {
				return fmt.Errorf("containerd-shim: opening file %q failed: %w", i.name, err)
			}
			p.file = file
			fw = file
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
	b := make([]byte, 1)
	if _, err := r.Read(b); err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read from logging binary: %w", err)
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

type LogTag string
type StreamType string

const (
	// delimiter used in CRI logging format.
	delimiter = ' '
	// eof is end-of-line.
	eol = '\n'
	// timestampFormat is the timestamp format used in CRI logging format.
	timestampFormat = time.RFC3339Nano
	// defaultBufSize is the default size of the read buffer in bytes.
	defaultBufSize = 4096

	// LogTagPartial means the line is part of multiple lines.
	LogTagPartial LogTag = "P"
	// LogTagFull means the line is a single full line or the end of multiple lines.
	LogTagFull LogTag = "F"
	// LogTagDelimiter is the delimiter for different log tags.
	LogTagDelimiter = ":"
)

func redirectLogs(path string, rc io.Reader, w io.Writer, s StreamType, maxLen int) {
	var (
		stream    = []byte(s)
		delimiter = []byte{delimiter}
		partial   = []byte(LogTagPartial)
		full      = []byte(LogTagFull)
		buf       [][]byte
		length    int
		bufSize   = defaultBufSize

		timeBuffer = make([]byte, len(timestampFormat))
		lineBuffer = bytes.Buffer{}
	)
	// Make sure bufSize <= maxLen
	if maxLen > 0 && maxLen < bufSize {
		bufSize = maxLen
	}
	r := bufio.NewReaderSize(rc, bufSize)
	writeLineBuffer := func(tag []byte, lineBytes [][]byte) {
		timeBuffer = time.Now().AppendFormat(timeBuffer[:0], timestampFormat)
		headers := [][]byte{timeBuffer, stream, tag}

		lineBuffer.Reset()
		for _, h := range headers {
			lineBuffer.Write(h)
			lineBuffer.Write(delimiter)
		}
		for _, l := range lineBytes {
			lineBuffer.Write(l)
		}
		lineBuffer.WriteByte(eol)
		if _, err := lineBuffer.WriteTo(w); err != nil {
			log.L.WithError(err).Errorf("Fail to write %q log to log file %q", s, path)
		}
	}
	for {
		var stop bool
		newLine, isPrefix, err := readLine(r)
		// NOTE(random-liu): readLine can return actual content even if there is an error.
		if len(newLine) > 0 {
			// Buffer returned by ReadLine will change after
			// next read, copy it.
			l := make([]byte, len(newLine))
			copy(l, newLine)
			buf = append(buf, l)
			length += len(l)
		}
		if err != nil {
			if err == io.EOF {
				log.L.Tracef("Getting EOF from stream %q while redirecting to log file %q", s, path)
			} else {
				log.L.WithError(err).Errorf("An error occurred when redirecting stream %q to log file %q", s, path)
			}
			if length == 0 {
				// No content left to write, break.
				break
			}
			// Stop after writing the content left in buffer.
			stop = true
		}
		if maxLen > 0 && length > maxLen {
			exceedLen := length - maxLen
			last := buf[len(buf)-1]
			if exceedLen > len(last) {
				// exceedLen must <= len(last), or else the buffer
				// should have be written in the previous iteration.
				panic("exceed length should <= last buffer size")
			}
			buf[len(buf)-1] = last[:len(last)-exceedLen]
			writeLineBuffer(partial, buf)
			buf = [][]byte{last[len(last)-exceedLen:]}
			length = exceedLen
		}
		if isPrefix {
			continue
		}
		if stop {
			// readLine only returns error when the message doesn't
			// end with a newline, in that case it should be treated
			// as a partial line.
			writeLineBuffer(partial, buf)
		} else {
			writeLineBuffer(full, buf)
		}
		buf = nil
		length = 0
		if stop {
			break
		}
	}
	log.L.Debugf("Finish redirecting stream %q to log file %q", s, path)
}

func readLine(b *bufio.Reader) (line []byte, isPrefix bool, err error) {
	line, err = b.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		// Handle the case where "\r\n" straddles the buffer.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			// Unread the last '\r'
			if err := b.UnreadByte(); err != nil {
				panic(fmt.Sprintf("invalid unread %v", err))
			}
			line = line[:len(line)-1]
		}
		return line, true, nil
	}

	if len(line) == 0 {
		if err != nil {
			line = nil
		}
		return
	}

	if line[len(line)-1] == '\n' {
		// "ReadSlice returns err != nil if and only if line does not end in delim"
		// (See https://golang.org/pkg/bufio/#Reader.ReadSlice).
		if err != nil {
			panic(fmt.Sprintf("full read with unexpected error %v", err))
		}
		drop := 1
		if len(line) > 1 && line[len(line)-2] == '\r' {
			drop = 2
		}
		line = line[:len(line)-drop]
	}
	return
}

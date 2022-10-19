//go:build !windows
// +build !windows

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

package io

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/containerd/containerd/pkg/stdio"
	"github.com/containerd/go-runc"
)

type fifo struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (f *fifo) Close() {
	if f.r != nil {
		f.r.Close()
	}
	if f.w != nil {
		f.w.Close()
	}
}

type fifoIO struct {
	in  *fifo
	out *fifo
	err *fifo
}

func (i *fifoIO) Stdin() io.WriteCloser {
	if i.in != nil && i.in.w != nil {
		return i.in.w
	}
	return nil
}

func (i *fifoIO) Stdout() io.ReadCloser {
	if i.out != nil && i.out.r != nil {
		return i.out.r
	}
	return nil
}

func (i *fifoIO) Stderr() io.ReadCloser {
	if i.err != nil && i.err.r != nil {
		return i.err.r
	}
	return nil
}

func (i *fifoIO) Close() error {
	if i.in != nil {
		i.in.Close()
	}
	if i.out != nil {
		i.out.Close()
	}
	if i.err != nil {
		i.err.Close()
	}
	return nil
}

func (i *fifoIO) CloseAfterStart() error {
	if i.in != nil && i.in.r != nil {
		i.in.r.Close()
	}
	if i.out != nil && i.out.w != nil {
		i.out.w.Close()
	}
	if i.err != nil && i.err.w != nil {
		i.err.w.Close()
	}
	return nil
}

// Set sets the io to the exec.Cmd
func (i *fifoIO) Set(cmd *exec.Cmd) {
	if i.in != nil && i.in.r != nil {
		cmd.Stdin = i.in.r
	}
	if i.out != nil && i.out.w != nil {
		cmd.Stdout = i.out.w
	}
	if i.err != nil && i.err.w != nil {
		cmd.Stderr = i.err.w
	}
}

func openFifo(file string, stdin bool) (*fifo, error) {
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			if err := syscall.Mkfifo(file, 0777); err != nil && !os.IsExist(err) {
				return nil, fmt.Errorf("error creating fifo %v: %w", file, err)
			}
		} else {
			return nil, err
		}
	}

	var (
		r                 io.ReadCloser
		w                 io.WriteCloser
		wg                sync.WaitGroup
		errRead, errWrite error
	)

	wg.Add(2)

	go func() {
		if stdin {
			r, errRead = os.OpenFile(file, syscall.O_RDWR, 0)
		} else {
			r, errRead = os.OpenFile(file, syscall.O_RDONLY, 0)
		}

		wg.Done()
	}()

	go func() {
		if !stdin {
			w, errWrite = os.OpenFile(file, syscall.O_RDWR, 0)
		} else {
			w, errWrite = os.OpenFile(file, syscall.O_WRONLY, 0)
		}
		wg.Done()
	}()
	wg.Wait()
	if errRead != nil || errWrite != nil {
		return nil, fmt.Errorf("create fifo failed: read[%v], write[%v]", errRead, errWrite)
	}
	return &fifo{
		r: r,
		w: w,
	}, nil
}

func NewFifoIO(stdio stdio.Stdio, uid, gid int) (i runc.IO, err error) {
	var (
		stdin, stdout, stderr *fifo
	)
	defer func() {
		if err != nil {
			if stdin != nil {
				stdin.Close()
			}
			if stdout != nil {
				stdout.Close()
			}
			if stderr != nil {
				stderr.Close()
			}
		}
	}()
	if stdio.Stdin != "" {
		stdin, err = openFifo(stdio.Stdin, true)
		if err != nil {
			return nil, err
		}
	}

	if stdio.Stdout != "" {
		stdout, err = openFifo(stdio.Stdout, false)
		if err != nil {
			return nil, err
		}
	}

	if stdio.Stderr != "" {
		stderr, err = openFifo(stdio.Stderr, false)
		if err != nil {
			return nil, err
		}
	}

	return &fifoIO{
		in:  stdin,
		out: stdout,
		err: stderr,
	}, nil
}

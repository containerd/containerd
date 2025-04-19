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
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/cio"
)

// AttachOptions specifies how to attach to a container.
type AttachOptions struct {
	Stdin     io.Reader
	Stdout    io.WriteCloser
	Stderr    io.WriteCloser
	Tty       bool
	StdinOnce bool
	// CloseStdin is the function to close container stdin.
	CloseStdin func() error
}

// StreamType is the type of the stream, stdout/stderr.
type StreamType string

const (
	// Stdin stream type.
	Stdin StreamType = "stdin"
	// Stdout stream type.
	Stdout = StreamType(runtime.Stdout)
	// Stderr stream type.
	Stderr = StreamType(runtime.Stderr)
)

type wgCloser struct {
	ctx    context.Context
	wg     *sync.WaitGroup
	set    []io.Closer
	cancel context.CancelFunc
}

func (g *wgCloser) Wait() {
	g.wg.Wait()
}

func (g *wgCloser) Close() {
	for _, f := range g.set {
		f.Close()
	}
}

func (g *wgCloser) Cancel() {
	g.cancel()
}

// newFifos creates fifos directory for a container.
func newFifos(root, id string, tty, stdin bool) (*cio.FIFOSet, error) {
	root = filepath.Join(root, "io")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	fifos, err := cio.NewFIFOSetInDir(root, id, tty)
	if err != nil {
		return nil, err
	}
	if !stdin {
		fifos.Stdin = ""
	}
	return fifos, nil
}

// newStreams init streams for io of container.
func newStreams(address, id string, tty, stdin bool) (*cio.FIFOSet, error) {
	fifos := cio.NewFIFOSet(cio.Config{}, func() error { return nil })
	if stdin {
		streamID := id + "-stdin"
		fifos.Stdin = fmt.Sprintf("%s?streaming_id=%s", address, streamID)
	}
	stdoutStreamID := id + "-stdout"
	fifos.Stdout = fmt.Sprintf("%s?streaming_id=%s", address, stdoutStreamID)
	if !tty {
		stderrStreamID := id + "-stderr"
		fifos.Stderr = fmt.Sprintf("%s?streaming_id=%s", address, stderrStreamID)
	}
	fifos.Terminal = tty
	return fifos, nil
}

type stdioStream struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// newStdioStream creates actual streams or fifos for stdio.
func newStdioStream(fifos *cio.FIFOSet) (_ *stdioStream, _ *wgCloser, err error) {
	var (
		set         []io.Closer
		ctx, cancel = context.WithCancel(context.Background())
		p           = &stdioStream{}
	)
	defer func() {
		if err != nil {
			for _, f := range set {
				f.Close()
			}
			cancel()
		}
	}()

	if fifos.Stdin != "" {
		in, err := openStdin(ctx, fifos.Stdin)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open stdin, %w", err)
		}
		p.stdin = in
		set = append(set, in)
	}

	if fifos.Stdout != "" {
		out, err := openOutput(ctx, fifos.Stdout)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open stdout, %w", err)
		}
		p.stdout = out
		set = append(set, out)
	}

	if fifos.Stderr != "" {
		out, err := openOutput(ctx, fifos.Stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open stderr, %w", err)
		}
		p.stderr = out
		set = append(set, out)
	}

	return p, &wgCloser{
		wg:     &sync.WaitGroup{},
		set:    set,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func openStdin(ctx context.Context, url string) (io.WriteCloser, error) {
	ok := strings.Contains(url, "://")
	if !ok {
		return openPipe(ctx, url, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	}

	return openStdinStream(ctx, url)
}

func openOutput(ctx context.Context, url string) (io.ReadCloser, error) {
	ok := strings.Contains(url, "://")
	if !ok {
		return openPipe(ctx, url, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	}

	return openOutputStream(ctx, url)
}

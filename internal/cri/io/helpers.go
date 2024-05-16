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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/ttrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	streamingapi "github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/streaming/proxy"
	"github.com/containerd/containerd/v2/core/transfer/streaming"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/shim"
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

func openStdinStream(ctx context.Context, url string) (io.WriteCloser, error) {
	stream, err := openStream(ctx, url)
	if err != nil {
		return nil, err
	}
	return streaming.WriteByteStream(ctx, stream), nil
}

func openOutput(ctx context.Context, url string) (io.ReadCloser, error) {
	ok := strings.Contains(url, "://")
	if !ok {
		return openPipe(ctx, url, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	}

	return openOutputStream(ctx, url)
}

func openOutputStream(ctx context.Context, url string) (io.ReadCloser, error) {
	stream, err := openStream(ctx, url)
	if err != nil {
		return nil, err
	}
	return streaming.ReadByteStream(ctx, stream), nil
}

func openStream(ctx context.Context, urlStr string) (streamingapi.Stream, error) {
	// urlStr should be in the form of:
	// <ttrpc|grpc>+<unix|vsock|hvsock>://<uds-path|vsock-cid:vsock-port|uds-path:hvsock-port>?streaming_id=<stream-id>
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("address url parse error: %v", err)
	}
	// The address returned from sandbox controller should be in the form like ttrpc+unix://<uds-path>
	// or grpc+vsock://<cid>:<port>, we should get the protocol from the url first.
	protocol, scheme, ok := strings.Cut(u.Scheme, "+")
	if !ok {
		return nil, fmt.Errorf("the scheme of sandbox address should be in " +
			" the form of <protocol>+<unix|vsock|tcp>, i.e. ttrpc+unix or grpc+vsock")
	}

	id := u.Query().Get("streaming_id")
	if id == "" {
		return nil, fmt.Errorf("no stream id in url queries")
	}
	realAddress := fmt.Sprintf("%s://%s/%s", scheme, u.Host, u.Path)
	conn, err := shim.AnonReconnectDialer(realAddress, 100*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect the stream %v", err)
	}
	var stream streamingapi.Stream

	switch protocol {
	case "ttrpc":
		c := ttrpc.NewClient(conn)
		streamCreator := proxy.NewStreamCreator(c)
		stream, err = streamCreator.Create(ctx, id)
		if err != nil {
			return nil, err
		}
		return stream, nil

	case "grpc":
		ctx, cancel := context.WithTimeout(ctx, time.Second*100)
		defer cancel()

		gopts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		}
		conn, err := grpc.DialContext(ctx, realAddress, gopts...)
		if err != nil {
			return nil, err
		}
		streamCreator := proxy.NewStreamCreator(conn)
		stream, err = streamCreator.Create(ctx, id)
		if err != nil {
			return nil, err
		}
		return stream, nil
	default:
		return nil, fmt.Errorf("protocol not supported")
	}
}

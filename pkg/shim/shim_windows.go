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

package shim

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
)

func setupSignals(config Config) (chan os.Signal, error) {
	signals := make(chan os.Signal, 32)
	signal.Notify(signals, os.Interrupt, os.Kill)
	return signals, nil
}

func newServer(opts ...ttrpc.ServerOpt) (*ttrpc.Server, error) {
	return ttrpc.NewServer(opts...)
}

func subreaper() error {
	// Windows doesn't have a subreaper concept like Linux
	return nil
}

func setupDumpStacks(dump chan<- os.Signal) {
	// Windows doesn't have SIGUSR1, so we don't set up stack dumps
}

func serveListener(path string, fd uintptr) (net.Listener, error) {
	if path == "" {
		// On Windows, we can't inherit file descriptors like Unix
		// Instead, check for socket path in environment variable
		path = os.Getenv("TTRPC_SOCKET")
		if path == "" {
			// Try to read from DEBUG_SOCKET for debug socket
			path = os.Getenv("DEBUG_SOCKET")
		}
		if path == "" {
			return nil, fmt.Errorf("no socket path provided and TTRPC_SOCKET env not set")
		}
		log.L.WithField("pipe", path).Debug("using pipe path from environment")
	}

	// On Windows, path should be a named pipe path
	// If it looks like a Unix socket path, skip it (this shouldn't happen)
	if len(path) > 0 && path[0] == '/' {
		log.L.WithField("path", path).Debug("Ignoring Unix-style socket path on Windows")
		return nil, fmt.Errorf("unix-style socket path not supported on Windows: %s", path)
	}

	// Listen on the named pipe
	l, err := winio.ListenPipe(path, nil)
	if err != nil {
		return nil, err
	}
	log.L.WithField("pipe", path).Debug("serving api on named pipe")
	return l, nil
}

func reap(ctx context.Context, logger *log.Entry, signals chan os.Signal) error {
	logger.Debug("starting signal loop (Windows - no reaping needed)")

	// Windows automatically cleans up child processes, no reaping needed
	// Just wait for context cancellation
	<-ctx.Done()
	return ctx.Err()
}

func handleExitSignals(ctx context.Context, logger *log.Entry, cancel context.CancelFunc) {
	ch := make(chan os.Signal, 32)
	signal.Notify(ch, os.Interrupt, os.Kill)

	for {
		select {
		case s := <-ch:
			logger.WithField("signal", s).Debug("Caught exit signal")
			cancel()
			return
		case <-ctx.Done():
			return
		}
	}
}

func openLog(ctx context.Context, _ string) (io.Writer, error) {
	// On Windows, just return stderr for logging
	// We don't have FIFO support like Unix
	return os.Stderr, nil
}

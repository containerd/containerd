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
	"strings"
	"sync/atomic"
	"time"

	winio "github.com/Microsoft/go-winio"
	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"golang.org/x/sys/windows"
)

func setupSignals(config Config) (chan os.Signal, error) {
	return nil, errdefs.ErrNotImplemented
}

func newServer(opts ...ttrpc.ServerOpt) (*ttrpc.Server, error) {
	return nil, errdefs.ErrNotImplemented
}

func subreaper() error {
	return errdefs.ErrNotImplemented
}

func setupDumpStacks(dump chan<- os.Signal) {
}

var (
	modkernel32     = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole = modkernel32.NewProc("FreeConsole")
)

// Detachable is an optional extension of Shim for Windows shims that want to
// avoid spawning a separate long-lived daemon process.
//
// When the Shim passed to RunShim also implements Detachable, the framework
// calls Detach instead of Start. Detach binds the TTRPC endpoint in the
// current process and returns the bootstrap result. The framework then writes
// the result to stdout, closes stdout (so containerd unblocks), calls
// FreeConsole, and falls through to the serve loop.
//
// Implementations should call SetDetachListener with the pre-bound listener so
// serveListener can reuse it rather than rebinding, preserving any connections
// containerd queued after reading the result. Any transport is supported.
type Detachable interface {
	Shim
	// Detach binds the TTRPC endpoint in the current process and returns
	// the bootstrap result. Must not launch a child process.
	Detach(ctx context.Context, params *bootapi.BootstrapParams) (*bootapi.BootstrapResult, error)
}

// detachListener holds a net.Listener pre-bound by [Detachable.Detach].
var detachListener atomic.Pointer[net.Listener]

// SetDetachListener stores a pre-bound listener so that serveListener can
// reuse it rather than rebinding. The listener must already be bound.
// Storing it before stdout is closed ensures containerd can connect the
// instant it reads the bootstrap result, regardless of transport.
func SetDetachListener(l net.Listener) {
	detachListener.Store(&l)
}

// takeDetachListener retrieves and clears the listener stored by
// SetDetachListener. Returns nil if no listener was pre-bound.
func takeDetachListener() net.Listener {
	p := detachListener.Swap(nil)
	if p == nil {
		return nil
	}
	return *p
}

// tryDetach calls Detach if manager implements Detachable, then writes the
// bootstrap result to stdout, closes stdout so containerd unblocks, and calls
// FreeConsole. Returns true so the caller falls through to the serve loop.
// Returns false if manager does not implement Detachable.
func tryDetach(ctx context.Context, manager Shim, params *bootapi.BootstrapParams) (bool, error) {
	dm, ok := manager.(Detachable)
	if !ok {
		return false, nil
	}

	result, err := dm.Detach(ctx, params)
	if err != nil {
		return false, err
	}

	data, err := proto.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("marshal bootstrap result: %w", err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return false, err
	}
	// Close stdout: containerd's reader returns at EOF, allowing it to
	// proceed without waiting for this process to exit.
	os.Stdout.Close()
	// Detach from any inherited console so that OS signals (e.g. Ctrl+C)
	// do not reach the self-daemonized process.
	if err := detachConsole(); err != nil {
		log.G(ctx).WithError(err).Debug("detach console (non-fatal)")
	}
	return true, nil
}

// serveListener creates the TTRPC listener for the shim daemon.
//
// On Windows two transport modes are supported:
//
//   - Named pipe (\\.\pipe\…): the historical default for Windows shims.
//
//   - AF_UNIX socket (unix://…): preferred for Detachable shims
//     because it eliminates the named-pipe polling overhead.
//     AF_UNIX is available on Windows 10 build 17063+.
//
// If Detachable.Detach pre-bound a listener via SetDetachListener, it is
// returned directly so that connections queued while the "start" process
// was still running are not lost.
//
// Note: Windows does not support inheriting a listener via fd (Unix passes
// fd 3/4 for this purpose); the fd parameter is unused on Windows.
func serveListener(path string, _ uintptr) (net.Listener, error) {
	if path == "" {
		path = os.Getenv("TTRPC_SOCKET")
		if path == "" {
			return nil, fmt.Errorf("no socket path provided and TTRPC_SOCKET env not set")
		}
	}

	// If Detach pre-bound a listener, use it regardless of scheme. The shim
	// is responsible for matching the listener's transport to the address it
	// returned in BootstrapResult.
	if l := takeDetachListener(); l != nil {
		log.L.WithField("address", path).Debug("serving ttrpc on pre-bound listener (Detachable)")
		return l, nil
	}

	// AF_UNIX mode.
	if socketPath, ok := strings.CutPrefix(path, "unix://"); ok {
		os.Remove(socketPath) //nolint:errcheck,gosec // socketPath is the shim's own socket address
		l, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("listen unix %s: %w", socketPath, err)
		}
		log.L.WithField("socket", socketPath).Debug("serving ttrpc on AF_UNIX socket")
		return l, nil
	}

	// Named-pipe mode.
	if !strings.HasPrefix(path, `\\.\pipe\`) {
		return nil, fmt.Errorf("unsupported TTRPC_SOCKET address %q (expected unix:// or named pipe)", path)
	}
	l, err := winio.ListenPipe(path, nil)
	if err != nil {
		return nil, fmt.Errorf("listen named pipe %s: %w", path, err)
	}
	log.L.WithField("pipe", path).Debug("serving ttrpc on named pipe")
	return l, nil
}

func reap(ctx context.Context, logger *log.Entry, signals chan os.Signal) error {
	return errdefs.ErrNotImplemented
}

func handleExitSignals(ctx context.Context, logger *log.Entry, cancel context.CancelFunc) {
}

func openLog(ctx context.Context, _ string) (io.Writer, error) {
	return nil, errdefs.ErrNotImplemented
}

// detachConsole detaches the current process from its inherited console.
// After self-daemonizing (Detachable), the "start" process continues
// running as the shim daemon. Detaching prevents OS signals sent to the
// parent's console (e.g. Ctrl+C in a terminal) from being delivered to the
// daemon.
func detachConsole() error {
	r, _, err := procFreeConsole.Call()
	if r == 0 {
		return fmt.Errorf("FreeConsole: %w", err)
	}
	return nil
}

// awaitPipeReady polls a TTRPC endpoint address until it is connectable.
//
// For named pipes the daemon must call winio.ListenPipe before the pipe is
// visible to clients, so we poll with 10 ms sleep intervals for up to 5 s.
//
// For AF_UNIX sockets the socket file appears atomically when net.Listen
// returns. If the shim used Detachable and pre-bound the socket via
// SetDetachListener, it exists before stdout was even closed, so no polling
// is needed.
func awaitPipeReady(address string) error {
	if address == "" || strings.HasPrefix(address, "unix://") {
		// AF_UNIX: either pre-bound by Detach (no wait needed) or bound
		// atomically by the daemon (containerd's AnonDialer retries).
		return nil
	}
	if !strings.HasPrefix(address, `\\.\pipe\`) {
		// Unknown scheme — let the caller's dialer deal with it.
		return nil
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	var lastErr error
	for {
		// Use a 1s per-attempt timeout to avoid blocking indefinitely if
		// the pipe exists but all instances are busy.
		dialTimeout := time.Second
		conn, err := winio.DialPipe(address, &dialTimeout)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		// Retry on both "pipe not found" and "pipe busy / deadline exceeded"
		// — the pipe may still be starting up or temporarily at capacity.
		if !os.IsNotExist(err) && err != context.DeadlineExceeded {
			return err
		}
		select {
		case <-timer.C:
			return fmt.Errorf("pipe %s not ready after 5s: %w", address, lastErr)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

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

package integration

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	v2shimcli "github.com/containerd/containerd/v2/core/runtime/v2/shim"
	"github.com/containerd/ttrpc"
)

const abstractSocketPrefix = "\x00"

// TestFailFastWhenConnectShim is to test that the containerd task manager
// should not tolerate ENOENT during restarting. In linux, the containerd shim
// always listens on socket before task manager dial. If there is ENOENT or
// ECONNREFUSED error, the task manager should clean up because that socket file
// is gone or shim doesn't listen on the socket anymore.
func TestFailFastWhenConnectShim(t *testing.T) {
	t.Parallel()

	// abstract Unix domain sockets are only for Linux.
	if runtime.GOOS == "linux" {
		t.Run("abstract-unix-socket-v2", testFailFastWhenConnectShim(true, v2shimcli.AnonDialer))
	}
	t.Run("normal-unix-socket-v2", testFailFastWhenConnectShim(false, v2shimcli.AnonDialer))
}

type dialFunc func(address string, timeout time.Duration) (net.Conn, error)

func testFailFastWhenConnectShim(abstract bool, dialFn dialFunc) func(*testing.T) {
	return func(t *testing.T) {
		var (
			ctx                     = context.Background()
			addr, listener, cleanup = newTestListener(t, abstract)
			errCh                   = make(chan error, 1)

			checkDialErr = func(addr string, errCh chan error, expected error) {
				go func() {
					_, err := dialFn(addr, 1*time.Hour)
					errCh <- err
				}()

				select {
				case <-time.After(10 * time.Second):
					t.Fatalf("expected fail fast, but got timeout")
				case err := <-errCh:
					t.Helper()
					if !errors.Is(err, expected) {
						t.Fatalf("expected error %v, but got %v", expected, err)
					}
				}
			}
		)
		defer cleanup()
		defer listener.Close()

		ttrpcSrv, err := ttrpc.NewServer()
		if err != nil {
			t.Fatalf("failed to new ttrpc server: %v", err)
		}
		go func() {
			ttrpcSrv.Serve(ctx, listener)
		}()

		// ttrpcSrv starts in other goroutine so that we need to retry AnonDialer
		// here until ttrpcSrv receives the request.
		go func() {
			to := time.After(10 * time.Second)

			for {
				select {
				case <-to:
					errCh <- errors.New("timeout")
					return
				default:
				}

				conn, err := dialFn(addr, 1*time.Hour)
				if err != nil {
					if errors.Is(err, syscall.ECONNREFUSED) {
						time.Sleep(10 * time.Millisecond)
						continue
					}
					errCh <- err
					return
				}

				conn.Close()
				errCh <- nil
				return
			}
		}()

		// it should be successful
		if err := <-errCh; err != nil {
			t.Fatalf("failed to dial: %v", err)
		}

		// NOTE(fuweid):
		//
		// UnixListener will unlink that the socket file when call Close.
		// Disable unlink when close to keep the socket file.
		listener.(*net.UnixListener).SetUnlinkOnClose(false)

		listener.Close()
		ttrpcSrv.Shutdown(ctx)

		checkDialErr(addr, errCh, syscall.ECONNREFUSED)

		// remove the socket file
		cleanup()

		if abstract {
			checkDialErr(addr, errCh, syscall.ECONNREFUSED)
		} else {
			// should not wait for the socket file show up again.
			checkDialErr(addr, errCh, syscall.ENOENT)
		}
	}
}

func newTestListener(t testing.TB, abstract bool) (string, net.Listener, func()) {
	tmpDir := t.TempDir()

	// NOTE(fuweid):
	//
	// Before patch https://github.com/containerd/containerd/commit/bd908acabd1a31c8329570b5283e8fdca0b39906,
	// The shim stores the abstract socket file without abstract socket
	// prefix and `unix://`. For the existing shim, if the socket file
	// only contains the path, it will indicate that it is abstract socket.
	// Otherwise, it will be normal socket file formated in `unix:///xyz'.
	addr := filepath.Join(tmpDir, "uds.socket")
	if abstract {
		addr = abstractSocketPrefix + addr
	} else {
		addr = "unix://" + addr
	}

	listener, err := net.Listen("unix", strings.TrimPrefix(addr, "unix://"))
	if err != nil {
		t.Fatalf("failed to listen on %s: %v", addr, err)
	}

	return strings.TrimPrefix(addr, abstractSocketPrefix), listener, func() {
		os.RemoveAll(tmpDir)
	}
}

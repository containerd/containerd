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

package dialer

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// acceptAndClose accepts connections on l and closes them, until l is closed.
func acceptAndClose(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}
}

// listenUnix listens on an AF_UNIX socket in a directory with a short path:
// t.TempDir embeds the test name, which can overflow the sun_path limit (104
// on macOS).
func listenUnix(t *testing.T, name string) (net.Listener, string) {
	t.Helper()

	dir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, name)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q) failed: %v", sockPath, err)
	}
	t.Cleanup(func() { l.Close() })
	return l, sockPath
}

func TestDialerUnixSocket(t *testing.T) {
	for _, prefix := range []string{"", "unix://"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			l, sockPath := listenUnix(t, "test.sock")
			go acceptAndClose(l)

			conn, err := dialer(prefix+sockPath, 5*time.Second)
			if err != nil {
				t.Fatalf("dialer(%q) failed: %v", prefix+sockPath, err)
			}
			conn.Close()
		})
	}
}

// TestDialAddressGRPC verifies that gRPC accepts the target from DialAddress
// and connects through ContextDialer, as the containerd client does.
func TestDialAddressGRPC(t *testing.T) {
	l, sockPath := listenUnix(t, "test.sock")
	connectGRPC(t, l, sockPath)
}

// connectGRPC serves gRPC on l and connects to it at address.
func connectGRPC(t *testing.T, l net.Listener, address string) {
	t.Helper()

	srv := grpc.NewServer()
	go srv.Serve(l)
	t.Cleanup(srv.Stop)

	target := DialAddress(address)
	conn, err := grpc.NewClient(target,
		grpc.WithContextDialer(ContextDialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient(%q) failed: %v", target, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn.Connect()
	for state := conn.GetState(); state != connectivity.Ready; state = conn.GetState() {
		if !conn.WaitForStateChange(ctx, state) {
			t.Fatalf("connection to %q is %v, want %v: %v", target, state, connectivity.Ready, ctx.Err())
		}
	}
}

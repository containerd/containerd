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

package erofs

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
)

func TestMain(m *testing.M) {
	flag.Parse()

	// Short mode: run only the fast, in-process, network-free tests.
	if testing.Short() {
		os.Exit(m.Run())
	}

	// Create a temporary directory for the server's root, state, and socket.
	// This is cleaned up after the suite runs.
	tmpDir, err := os.MkdirTemp("", "containerd-erofs-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	sock := socketPath(tmpDir)
	address = sock

	ctx := context.Background() //nolint:all

	srv, err := startInProcessServer(ctx, tmpDir, sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start in-process containerd: %v\n", err)
		os.Exit(1)
	}
	defer srv.Stop()

	if err := waitAndConfigureServer(ctx, sock); err != nil {
		fmt.Fprintf(os.Stderr, "in-process containerd not ready: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// newTestClient opens a new containerd client connected to the in-process
// server. The client is closed automatically when the test finishes.
func newTestClient(t testing.TB) *containerd.Client {
	t.Helper()
	c, err := containerd.New(address,
		containerd.WithDefaultNamespace(testNamespace),
	)
	if err != nil {
		t.Fatalf("failed to create containerd client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

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

// Package erofs provides a cross-platform integration test suite for the EROFS
// image format and the EROFS differ/snapshotter.
//
// # Test organisation
//
// Tests are grouped by dependency level:
//
//   - convert_linux_test.go — converter round-trips using the in-process server.
//     Linux-only due to transitive dmverity import in the erofs differ plugin.
//   - snapshotter_linux_test.go — Prepare/Apply/Commit lifecycle on the EROFS
//     snapshotter. No kernel mount required; runs without root.
//   - exec_linux_test.go — tests that mount EROFS images and run container
//     workloads. Require root + the erofs kernel module.
//
// # Running
//
// To run cross-platform tests without the server (fast, CI-friendly):
//
//	go test ./integration/erofs/... -short
//
// To run the full suite (in-process server, no binary needed):
//
//	go test ./integration/erofs/...
package erofs

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log/logtest"
)

const testNamespace = "erofs-testing"

var (
	// address is the socket path set by TestMain to the per-run temp socket.
	address string
)

// testContext returns a context carrying the erofs-testing namespace and,
// when t is non-nil, a per-test logger.
func testContext(t testing.TB) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:all
	ctx = namespaces.WithNamespace(ctx, testNamespace)
	if t != nil {
		ctx = logtest.WithT(ctx, t)
	}
	return ctx, cancel
}

// isNetworkError returns true when err looks like a transient network failure
// (dial, DNS, TLS). Used to skip tests that depend on a remote registry when
// running in an offline environment.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, frag := range []string{
		"dial tcp", "connection refused", "no such host",
		"network unreachable", "timeout", "EOF",
		"TLS handshake timeout",
	} {
		if containsFrag(msg, frag) {
			return true
		}
	}
	return false
}

func containsFrag(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

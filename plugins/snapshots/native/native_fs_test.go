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

// Package native contains the native copy-on-write snapshotter.
//
// This file runs SnapshotterFSSuite against the native snapshotter. Each
// active snapshot is a directory copy of its parent, so layer changes are
// applied with ordinary filesystem operations and inspected through fs.FS.
// Ownership changes require privileges, so the test requires root.
package native

import (
	"context"
	"runtime"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

// newFSSnapshotter creates a native snapshotter for the fs.FS test suite.
func newFSSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	sn, err := NewSnapshotter(root)
	if err != nil {
		return nil, nil, err
	}
	return sn, func() error { return sn.Close() }, nil
}

// TestNativeFS runs the fs.FS-based snapshot test suite against the native
// snapshotter. It requires root for ownership operations.
func TestNativeFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native snapshotter is only supported on Linux")
	}
	testutil.RequiresRoot(t)
	testsuite.SnapshotterFSSuite(t, "Native", newFSSnapshotter)
}

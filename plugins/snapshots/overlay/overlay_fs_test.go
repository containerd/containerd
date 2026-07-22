//go:build linux

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

// Package overlay contains the overlay snapshotter.
//
// This file runs SnapshotterFSSuite against the overlay snapshotter. It writes
// layers to the overlayfs upperdir directly and inspects snapshots through
// fs.FS. Overlay whiteouts and ownership changes require privileges, so the
// test requires root.
package overlay

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

// newFSSnapshotter creates an overlay snapshotter for the fs.FS test suite.
func newFSSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	sn, err := NewSnapshotter(root)
	if err != nil {
		return nil, nil, err
	}
	return sn, func() error { return sn.Close() }, nil
}

// TestOverlayFS runs the fs.FS-based snapshot test suite against the overlay
// snapshotter. It requires root for whiteout and ownership operations.
func TestOverlayFS(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterFSSuite(t, "overlayfs", newFSSnapshotter)
}

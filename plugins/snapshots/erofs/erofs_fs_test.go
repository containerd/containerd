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

// Package erofs contains the erofs snapshotter.
//
// This file runs SnapshotterFSSuite against the erofs snapshotter. It builds
// layers as EROFS images and inspects snapshots through fs.FS, so it needs no
// kernel mounts, no root, and no Linux-specific features; it runs on all
// platforms where the snapshotter can be instantiated.
package erofs

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"

	// Register the fsview erofs handler so that FSMounts can open erofs images.
	_ "github.com/containerd/containerd/v2/plugins/mount/fsview/erofs"
)

// newFSSnapshotter creates an erofs snapshotter for the fs.FS test suite.
func newFSSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	sn, err := NewSnapshotter(root)
	if err != nil {
		return nil, nil, err
	}
	return sn, func() error { return sn.Close() }, nil
}

// TestErofsFS runs the fs.FS-based snapshot test suite against the erofs
// snapshotter. No root privileges are required.
func TestErofsFS(t *testing.T) {
	testsuite.SnapshotterFSSuite(t, "erofs", newFSSnapshotter)
}

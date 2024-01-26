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

package blockfile

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/containerd/v2/snapshots/testsuite"
)

func newSnapshotter(t *testing.T) func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	opts, err := setupSnapshotter(t)
	if err != nil {
		t.Fatal("failed to get snapshotter options:", err)
	}

	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		snapshotter, err := NewSnapshotter(root, opts...)
		if err != nil {
			return nil, nil, err
		}

		return snapshotter, func() error { return snapshotter.Close() }, nil
	}
}

func TestBlockfile(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Blockfile", newSnapshotter(t))
}

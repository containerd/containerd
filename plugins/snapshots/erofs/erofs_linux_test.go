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
	"os/exec"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

func newSnapshotter(t *testing.T) func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	_, err := exec.LookPath("mkfs.erofs")
	if err != nil {
		t.Skipf("could not find mkfs.erofs: %v", err)
	}

	if !findErofs() {
		t.Skip("check for erofs kernel support failed, skipping test")
	}
	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		var opts []Opt

		snapshotter, err := NewSnapshotter(root, opts...)
		if err != nil {
			return nil, nil, err
		}

		return snapshotter, func() error { return snapshotter.Close() }, nil
	}
}

func TestErofs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "erofs", newSnapshotter(t))
}

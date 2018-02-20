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

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/naive"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/containerd/testutil"
)

func newTestSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	naiveRoot := filepath.Join(root, "naive")
	if err := os.Mkdir(naiveRoot, 0770); err != nil {
		return nil, nil, err
	}
	snapshotter, err := naive.NewSnapshotter(naiveRoot)
	if err != nil {
		return nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(root, "metadata.db"), 0660, nil)
	if err != nil {
		return nil, nil, err
	}

	sn := NewDB(db, nil, map[string]snapshots.Snapshotter{"naive": snapshotter}).Snapshotter("naive")

	return sn, func() error {
		if err := sn.Close(); err != nil {
			return err
		}
		return db.Close()
	}, nil
}

func TestMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshotter not implemented on windows")
	}
	// Snapshot tests require mounting, still requires root
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Metadata", newTestSnapshotter)
}

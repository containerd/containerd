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

// metadata_test is used to test the metadata snapshotter implementations
// against the snapshotter test suite.
package metadata_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	bolt "go.etcd.io/bbolt"
)

func newTestSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	nativeRoot := filepath.Join(root, "native")
	if err := os.Mkdir(nativeRoot, 0770); err != nil {
		return nil, nil, err
	}
	snapshotter, err := native.NewSnapshotter(nativeRoot)
	if err != nil {
		return nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(root, "metadata.db"), 0660, nil)
	if err != nil {
		return nil, nil, err
	}

	sn := metadata.NewDB(db, nil, map[string]snapshots.Snapshotter{"native": snapshotter}).Snapshotter("native")

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

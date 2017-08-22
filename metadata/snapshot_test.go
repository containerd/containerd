package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/naive"
	"github.com/containerd/containerd/snapshot/testsuite"
	"github.com/containerd/containerd/testutil"
)

func newSnapshotter(ctx context.Context, root string) (snapshot.Snapshotter, func() error, error) {
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

	sn := NewSnapshotter(db, "naive", snapshotter)

	return sn, func() error {
		return db.Close()
	}, nil
}

func TestMetadata(t *testing.T) {
	// Snapshot tests require mounting, still requires root
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Metadata", newSnapshotter)
}

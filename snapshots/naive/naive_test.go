package naive

import (
	"context"
	"runtime"
	"testing"

	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/containerd/testutil"
)

func newSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	snapshotter, err := NewSnapshotter(root)
	if err != nil {
		return nil, nil, err
	}

	return snapshotter, func() error { return snapshotter.Close() }, nil
}

func TestNaive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshotter not implemented on windows")
	}
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Naive", newSnapshotter)
}

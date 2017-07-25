package containerd

import (
	"context"
	"testing"

	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/testsuite"
)

func newSnapshotter(ctx context.Context, root string) (snapshot.Snapshotter, func(), error) {
	client, err := New(address)
	if err != nil {
		return nil, nil, err
	}

	sn := client.SnapshotService("")

	return sn, func() {
		client.Close()
	}, nil
}

func TestSnapshotterClient(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	testsuite.SnapshotterSuite(t, "SnapshotterClient", newSnapshotter)
}

package aufs

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/containerd/testutil"
)

func init() {
	// start a reaper to the tests because aufs execs modprobe
	s := make(chan os.Signal)
	signal.Notify(s, syscall.SIGCHLD)
	go func() {
		for range s {
			reaper.Reap()
		}
	}()
}

func TestAufs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Aufs", func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		s, err := New(root)
		if err != nil {
			return nil, nil, err
		}
		return s, s.Close, nil
	})
}

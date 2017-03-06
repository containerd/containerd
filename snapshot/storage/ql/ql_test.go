package ql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/docker/containerd/snapshot/storage"
	"github.com/docker/containerd/snapshot/storage/testsuite"
)

func BenchmarkSuite(b *testing.B) {
	testsuite.Benchmarks(b, "QL", func(ctx context.Context, root string) (storage.MetaStore, error) {
		return NewMetaStore(ctx, filepath.Join(root, "metadata.db"))
	})
}

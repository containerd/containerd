package testsuite

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/docker/containerd/snapshot/storage"
)

func Benchmarks(b *testing.B, name string, metaFn func(context.Context, string) (storage.MetaStore, error)) {
	b.Run("StatActive", makeBench(b, name, metaFn, statActiveBenchmark))
	b.Run("StatCommitted", makeBench(b, name, metaFn, statCommittedBenchmark))
	b.Run("CreateActive", makeBench(b, name, metaFn, createActiveBenchmark))
	b.Run("Remove", makeBench(b, name, metaFn, removeBenchmark))
	b.Run("Commit", makeBench(b, name, metaFn, commitBenchmark))
	b.Run("GetActive", makeBench(b, name, metaFn, getActiveBenchmark))
}

func makeBench(b *testing.B, name string, metaFn func(context.Context, string) (storage.MetaStore, error), fn func(context.Context, *testing.B, storage.MetaStore)) func(b *testing.B) {
	return func(b *testing.B) {
		ctx := context.Background()
		tmpDir, err := ioutil.TempDir("", "metastore-bench-"+name+"-")
		if err != nil {
			b.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ms, err := metaFn(ctx, tmpDir)
		if err != nil {
			b.Fatal(err)
		}

		// Fill database with 100 entries
		//for i := 0; i < 10000; i++ {
		//	if err := ms.CreateActive(ctx, fmt.Sprintf("prefill-%d", i), storage.CreateActiveOpts{}); err != nil {
		//		b.Fatal(err)
		//	}
		//}

		b.ResetTimer()
		fn(ctx, b, ms)
	}
}

func createActiveFromBase(ctx context.Context, ms storage.MetaStore, active, base string) error {
	if err := ms.CreateActive(ctx, "bottom", storage.CreateActiveOpts{}); err != nil {
		return err
	}
	if err := ms.Commit(ctx, "bottom", storage.CommitOpts{Name: base}); err != nil {
		return err
	}

	return ms.CreateActive(ctx, active, storage.CreateActiveOpts{Parent: base})
}

func statActiveBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	if err := createActiveFromBase(ctx, ms, "active", "base"); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ms.Stat(ctx, "active")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func statCommittedBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	if err := createActiveFromBase(ctx, ms, "active", "base"); err != nil {
		b.Fatal(err)
	}
	if err := ms.Commit(ctx, "active", storage.CommitOpts{Name: "committed"}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ms.Stat(ctx, "committed")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func createActiveBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	for i := 0; i < b.N; i++ {
		if err := ms.CreateActive(ctx, "active", storage.CreateActiveOpts{}); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := ms.Remove(ctx, "active", storage.RemoveOpts{}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func removeBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := ms.CreateActive(ctx, "active", storage.CreateActiveOpts{}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := ms.Remove(ctx, "active", storage.RemoveOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func commitBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		if err := ms.CreateActive(ctx, "active", storage.CreateActiveOpts{}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := ms.Commit(ctx, "active", storage.CommitOpts{Name: "committed"}); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := ms.Remove(ctx, "committed", storage.RemoveOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func getActiveBenchmark(ctx context.Context, b *testing.B, ms storage.MetaStore) {
	var base string
	for i := 1; i <= 10; i++ {
		if err := ms.CreateActive(ctx, "tmp", storage.CreateActiveOpts{Parent: base}); err != nil {
			b.Fatalf("create active failed: %+v", err)
		}
		base = fmt.Sprintf("base-%d", i)
		if err := ms.Commit(ctx, "tmp", storage.CommitOpts{Name: base}); err != nil {
			b.Fatalf("commit failed: %+v", err)
		}

	}

	if err := ms.CreateActive(ctx, "active", storage.CreateActiveOpts{Parent: base}); err != nil {
		b.Fatalf("create active failed: %+v", err)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := ms.GetActive(ctx, "active"); err != nil {
			b.Fatal(err)
		}
	}
}

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

package storage

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/containerd/containerd/snapshots"
)

// Benchmarks returns a benchmark suite using the provided metadata store
// creation method
func Benchmarks(b *testing.B, name string, metaFn metaFactory) {
	b.Run("StatActive", makeBench(b, name, metaFn, statActiveBenchmark))
	b.Run("StatCommitted", makeBench(b, name, metaFn, statCommittedBenchmark))
	b.Run("CreateActive", makeBench(b, name, metaFn, createActiveBenchmark))
	b.Run("Remove", makeBench(b, name, metaFn, removeBenchmark))
	b.Run("Commit", makeBench(b, name, metaFn, commitBenchmark))
	b.Run("GetActive", makeBench(b, name, metaFn, getActiveBenchmark))
	b.Run("WriteTransaction", openCloseWritable(b, name, metaFn))
	b.Run("ReadTransaction", openCloseReadonly(b, name, metaFn))
}

// makeBench creates a benchmark with a writable transaction
func makeBench(b *testing.B, name string, metaFn metaFactory, fn func(context.Context, *testing.B, *MetaStore)) func(b *testing.B) {
	return func(b *testing.B) {
		ctx := context.Background()
		tmpDir, err := ioutil.TempDir("", "metastore-bench-"+name+"-")
		if err != nil {
			b.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ms, err := metaFn(tmpDir)
		if err != nil {
			b.Fatal(err)
		}

		ctx, t, err := ms.TransactionContext(ctx, true)
		if err != nil {
			b.Fatal(err)
		}
		defer t.Commit()

		b.ResetTimer()
		fn(ctx, b, ms)
	}
}

func openCloseWritable(b *testing.B, name string, metaFn metaFactory) func(b *testing.B) {
	return func(b *testing.B) {
		ctx := context.Background()
		tmpDir, err := ioutil.TempDir("", "metastore-bench-"+name+"-")
		if err != nil {
			b.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ms, err := metaFn(tmpDir)
		if err != nil {
			b.Fatal(err)
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, t, err := ms.TransactionContext(ctx, true)
			if err != nil {
				b.Fatal(err)
			}
			if err := t.Commit(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func openCloseReadonly(b *testing.B, name string, metaFn metaFactory) func(b *testing.B) {
	return func(b *testing.B) {
		ctx := context.Background()
		tmpDir, err := ioutil.TempDir("", "metastore-bench-"+name+"-")
		if err != nil {
			b.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ms, err := metaFn(tmpDir)
		if err != nil {
			b.Fatal(err)
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, t, err := ms.TransactionContext(ctx, false)
			if err != nil {
				b.Fatal(err)
			}
			if err := t.Rollback(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func createActiveFromBase(ctx context.Context, ms *MetaStore, active, base string) error {
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "bottom", ""); err != nil {
		return err
	}
	if _, err := CommitActive(ctx, "bottom", base, snapshots.Usage{}); err != nil {
		return err
	}

	_, err := CreateSnapshot(ctx, snapshots.KindActive, active, base)
	return err
}

func statActiveBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	if err := createActiveFromBase(ctx, ms, "active", "base"); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := GetInfo(ctx, "active")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func statCommittedBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	if err := createActiveFromBase(ctx, ms, "active", "base"); err != nil {
		b.Fatal(err)
	}
	if _, err := CommitActive(ctx, "active", "committed", snapshots.Usage{}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := GetInfo(ctx, "committed")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func createActiveBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	for i := 0; i < b.N; i++ {
		if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active", ""); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if _, _, err := Remove(ctx, "active"); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func removeBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active", ""); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, _, err := Remove(ctx, "active"); err != nil {
			b.Fatal(err)
		}
	}
}

func commitBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active", ""); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := CommitActive(ctx, "active", "committed", snapshots.Usage{}); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if _, _, err := Remove(ctx, "committed"); err != nil {
			b.Fatal(err)
		}
	}
}

func getActiveBenchmark(ctx context.Context, b *testing.B, ms *MetaStore) {
	var base string
	for i := 1; i <= 10; i++ {
		if _, err := CreateSnapshot(ctx, snapshots.KindActive, "tmp", base); err != nil {
			b.Fatalf("create active failed: %+v", err)
		}
		base = fmt.Sprintf("base-%d", i)
		if _, err := CommitActive(ctx, "tmp", base, snapshots.Usage{}); err != nil {
			b.Fatalf("commit failed: %+v", err)
		}

	}

	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active", base); err != nil {
		b.Fatalf("create active failed: %+v", err)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := GetSnapshot(ctx, "active"); err != nil {
			b.Fatal(err)
		}
	}
}

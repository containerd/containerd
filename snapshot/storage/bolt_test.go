package storage

import (
	"path/filepath"
	"testing"

	// Does not require root but flag must be defined for snapshot tests
	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/storage/proto"
	_ "github.com/containerd/containerd/testutil"
)

func TestMetastore(t *testing.T) {
	MetaStoreSuite(t, "Metastore", func(root string) (*MetaStore, error) {
		return NewMetaStore(filepath.Join(root, "metadata.db"))
	})
}

// TestKidnConversion ensures we can blindly cast from protobuf types.
func TestKindConversion(t *testing.T) {
	for _, testcase := range []snapshot.Kind{
		snapshot.KindView,
		snapshot.KindActive,
		snapshot.KindCommitted,
	} {
		cast := proto.Kind(testcase)
		uncast := snapshot.Kind(cast)
		if uncast != testcase {
			t.Fatalf("kind value cast failed: %v != %v", uncast, testcase)
		}
	}
}

func BenchmarkSuite(b *testing.B) {
	Benchmarks(b, "BoltDBBench", func(root string) (*MetaStore, error) {
		return NewMetaStore(filepath.Join(root, "metadata.db"))
	})
}

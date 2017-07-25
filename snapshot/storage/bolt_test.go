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
	for _, testcase := range []struct {
		s snapshot.Kind
		p proto.Kind
	}{
		{
			s: snapshot.KindView,
			p: proto.KindView,
		},
		{
			s: snapshot.KindActive,
			p: proto.KindActive,
		},
		{
			s: snapshot.KindCommitted,
			p: proto.KindCommitted,
		},
	} {
		if testcase.s != snapshot.Kind(testcase.p) {
			t.Fatalf("snapshot kind value cast failed: %v != %v", testcase.s, testcase.p)
		}
		if testcase.p != proto.Kind(testcase.s) {
			t.Fatalf("proto kind value cast failed: %v != %v", testcase.s, testcase.p)
		}
	}
}

func BenchmarkSuite(b *testing.B) {
	Benchmarks(b, "BoltDBBench", func(root string) (*MetaStore, error) {
		return NewMetaStore(filepath.Join(root, "metadata.db"))
	})
}

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

package metadata

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/gc"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/gogo/protobuf/types"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

type testOptions struct {
	extraSnapshots map[string]func(string) (snapshots.Snapshotter, error)
}

type testOpt func(*testOptions)

func withSnapshotter(name string, fn func(string) (snapshots.Snapshotter, error)) testOpt {
	return func(to *testOptions) {
		if to.extraSnapshots == nil {
			to.extraSnapshots = map[string]func(string) (snapshots.Snapshotter, error){}
		}
		to.extraSnapshots[name] = fn
	}
}

func testDB(t *testing.T, opt ...testOpt) (context.Context, *DB, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")
	ctx = logtest.WithT(ctx, t)

	var topts testOptions

	for _, o := range opt {
		o(&topts)
	}

	dirname, err := ioutil.TempDir("", strings.Replace(t.Name(), "/", "_", -1)+"-")
	if err != nil {
		t.Fatal(err)
	}

	snapshotter, err := native.NewSnapshotter(filepath.Join(dirname, "native"))
	if err != nil {
		t.Fatal(err)
	}

	snapshotters := map[string]snapshots.Snapshotter{
		"native": snapshotter,
	}

	for name, fn := range topts.extraSnapshots {
		snapshotter, err := fn(filepath.Join(dirname, name))
		if err != nil {
			t.Fatal(err)
		}
		snapshotters[name] = snapshotter
	}

	cs, err := local.NewStore(filepath.Join(dirname, "content"))
	if err != nil {
		t.Fatal(err)
	}

	bdb, err := bolt.Open(filepath.Join(dirname, "metadata.db"), 0644, nil)
	if err != nil {
		t.Fatal(err)
	}

	db := NewDB(bdb, cs, snapshotters)
	if err := db.Init(ctx); err != nil {
		t.Fatal(err)
	}

	return ctx, db, func() {
		bdb.Close()
		if err := os.RemoveAll(dirname); err != nil {
			t.Log("failed removing temp dir", err)
		}
		cancel()
	}
}

func TestInit(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

	if err := NewDB(db, nil, nil).Init(ctx); err != nil {
		t.Fatal(err)
	}

	version, err := readDBVersion(db, bucketKeyVersion)
	if err != nil {
		t.Fatal(err)
	}
	if version != dbVersion {
		t.Fatalf("Unexpected version %d, expected %d", version, dbVersion)
	}
}

func TestMigrations(t *testing.T) {
	testRefs := []struct {
		ref  string
		bref string
	}{
		{
			ref:  "k1",
			bref: "bk1",
		},
		{
			ref:  strings.Repeat("longerkey", 30), // 270 characters
			bref: "short",
		},
		{
			ref:  "short",
			bref: strings.Repeat("longerkey", 30), // 270 characters
		},
		{
			ref:  "emptykey",
			bref: "",
		},
	}
	migrationTests := []struct {
		name  string
		init  func(*bolt.Tx) error
		check func(*bolt.Tx) error
	}{
		{
			name: "ChildrenKey",
			init: func(tx *bolt.Tx) error {
				bkt, err := createSnapshotterBucket(tx, "testing", "testing")
				if err != nil {
					return err
				}

				snapshots := []struct {
					key    string
					parent string
				}{
					{
						key:    "k1",
						parent: "",
					},
					{
						key:    "k2",
						parent: "k1",
					},
					{
						key:    "k2a",
						parent: "k1",
					},
					{
						key:    "a1",
						parent: "k2",
					},
				}

				for _, s := range snapshots {
					sbkt, err := bkt.CreateBucket([]byte(s.key))
					if err != nil {
						return err
					}
					if err := sbkt.Put(bucketKeyParent, []byte(s.parent)); err != nil {
						return err
					}
				}

				return nil
			},
			check: func(tx *bolt.Tx) error {
				bkt := getSnapshotterBucket(tx, "testing", "testing")
				if bkt == nil {
					return errors.Wrap(errdefs.ErrNotFound, "snapshots bucket not found")
				}
				snapshots := []struct {
					key      string
					children []string
				}{
					{
						key:      "k1",
						children: []string{"k2", "k2a"},
					},
					{
						key:      "k2",
						children: []string{"a1"},
					},
					{
						key:      "k2a",
						children: []string{},
					},
					{
						key:      "a1",
						children: []string{},
					},
				}

				for _, s := range snapshots {
					sbkt := bkt.Bucket([]byte(s.key))
					if sbkt == nil {
						return errors.Wrap(errdefs.ErrNotFound, "key does not exist")
					}

					cbkt := sbkt.Bucket(bucketKeyChildren)
					var cn int
					if cbkt != nil {
						cn = cbkt.Stats().KeyN
					}

					if cn != len(s.children) {
						return errors.Errorf("unexpected number of children %d, expected %d", cn, len(s.children))
					}

					for _, ch := range s.children {
						if v := cbkt.Get([]byte(ch)); v == nil {
							return errors.Errorf("missing child record for %s", ch)
						}
					}
				}

				return nil
			},
		},
		{
			name: "IngestUpdate",
			init: func(tx *bolt.Tx) error {
				bkt, err := createBucketIfNotExists(tx, bucketKeyVersion, []byte("testing"), bucketKeyObjectContent, deprecatedBucketKeyObjectIngest)
				if err != nil {
					return err
				}

				for _, s := range testRefs {
					if err := bkt.Put([]byte(s.ref), []byte(s.bref)); err != nil {
						return err
					}
				}

				return nil
			},
			check: func(tx *bolt.Tx) error {
				bkt := getIngestsBucket(tx, "testing")
				if bkt == nil {
					return errors.Wrap(errdefs.ErrNotFound, "ingests bucket not found")
				}

				for _, s := range testRefs {
					sbkt := bkt.Bucket([]byte(s.ref))
					if sbkt == nil {
						return errors.Wrap(errdefs.ErrNotFound, "ref does not exist")
					}

					bref := string(sbkt.Get(bucketKeyRef))
					if bref != s.bref {
						return errors.Errorf("unexpected reference key %q, expected %q", bref, s.bref)
					}
				}

				dbkt := getBucket(tx, bucketKeyVersion, []byte("testing"), bucketKeyObjectContent, deprecatedBucketKeyObjectIngest)
				if dbkt != nil {
					return errors.New("deprecated ingest bucket still exists")
				}

				return nil
			},
		},

		{
			name: "NoOp",
			init: func(tx *bolt.Tx) error {
				return nil
			},
			check: func(tx *bolt.Tx) error {
				return nil
			},
		},
	}

	if len(migrationTests) != len(migrations) {
		t.Fatal("Each migration must have a test case")
	}

	for i, mt := range migrationTests {
		t.Run(mt.name, runMigrationTest(i, mt.init, mt.check))
	}
}

func runMigrationTest(i int, init, check func(*bolt.Tx) error) func(t *testing.T) {
	return func(t *testing.T) {
		_, db, cancel := testEnv(t)
		defer cancel()

		if err := db.Update(init); err != nil {
			t.Fatal(err)
		}

		if err := db.Update(migrations[i].migrate); err != nil {
			t.Fatal(err)
		}

		if err := db.View(check); err != nil {
			t.Fatal(err)
		}
	}
}

func readDBVersion(db *bolt.DB, schema []byte) (int, error) {
	var version int
	if err := db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(schema)
		if bkt == nil {
			return errors.Wrap(errdefs.ErrNotFound, "no version bucket")
		}
		vb := bkt.Get(bucketKeyDBVersion)
		if vb == nil {
			return errors.Wrap(errdefs.ErrNotFound, "no version value")
		}
		v, _ := binary.Varint(vb)
		version = int(v)
		return nil
	}); err != nil {
		return 0, err
	}
	return version, nil
}

func TestMetadataCollector(t *testing.T) {
	mdb, cs, sn, cleanup := newStores(t)
	defer cleanup()

	var (
		ctx = logtest.WithT(context.Background(), t)

		objects = []object{
			blob(bytesFor(1), true),
			blob(bytesFor(2), false),
			blob(bytesFor(3), true),
			blob(bytesFor(4), false, "containerd.io/gc.root", time.Now().String()),
			newSnapshot("1", "", false, false),
			newSnapshot("2", "1", false, false),
			newSnapshot("3", "2", false, false),
			newSnapshot("4", "3", false, false),
			newSnapshot("5", "3", false, true),
			container("1", "4"),
			image("image-1", digestFor(2)),

			// Test lease preservation
			blob(bytesFor(5), false, "containerd.io/gc.ref.content.0", digestFor(6).String()),
			blob(bytesFor(6), false),
			blob(bytesFor(7), false),
			newSnapshot("6", "", false, false, "containerd.io/gc.ref.content.0", digestFor(7).String()),
			lease("lease-1", []leases.Resource{
				{
					ID:   digestFor(5).String(),
					Type: "content",
				},
				{
					ID:   "6",
					Type: "snapshots/native",
				},
			}, false),

			// Test flat lease
			blob(bytesFor(8), false, "containerd.io/gc.ref.content.0", digestFor(9).String()),
			blob(bytesFor(9), true),
			blob(bytesFor(10), true),
			newSnapshot("7", "", false, false, "containerd.io/gc.ref.content.0", digestFor(10).String()),
			newSnapshot("8", "7", false, false),
			newSnapshot("9", "8", false, false),
			lease("lease-2", []leases.Resource{
				{
					ID:   digestFor(8).String(),
					Type: "content",
				},
				{
					ID:   "9",
					Type: "snapshots/native",
				},
			}, false, "containerd.io/gc.flat", time.Now().String()),
		}
		remaining []gc.Node
	)

	if err := mdb.Update(func(tx *bolt.Tx) error {
		for _, obj := range objects {
			node, err := create(obj, tx, mdb, cs, sn)
			if err != nil {
				return err
			}
			if node != nil {
				remaining = append(remaining, *node)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Creation failed: %+v", err)
	}

	if _, err := mdb.GarbageCollect(ctx); err != nil {
		t.Fatal(err)
	}

	var actual []gc.Node

	if err := mdb.View(func(tx *bolt.Tx) error {
		scanFn := func(ctx context.Context, node gc.Node) error {
			actual = append(actual, node)
			return nil
		}
		return scanAll(ctx, tx, scanFn)
	}); err != nil {
		t.Fatal(err)
	}

	checkNodesEqual(t, actual, remaining)
}

func BenchmarkGarbageCollect(b *testing.B) {
	b.Run("10-Sets", benchmarkTrigger(10))
	b.Run("100-Sets", benchmarkTrigger(100))
	b.Run("1000-Sets", benchmarkTrigger(1000))
	b.Run("10000-Sets", benchmarkTrigger(10000))
}

func benchmarkTrigger(n int) func(b *testing.B) {
	return func(b *testing.B) {
		mdb, cs, sn, cleanup := newStores(b)
		defer cleanup()

		objects := []object{}

		// TODO: Allow max to be configurable
		for i := 0; i < n; i++ {
			objects = append(objects,
				blob(bytesFor(int64(i)), false),
				image(fmt.Sprintf("image-%d", i), digestFor(int64(i))),
			)
			lastSnapshot := 6
			for j := 0; j <= lastSnapshot; j++ {
				var parent string
				key := fmt.Sprintf("snapshot-%d-%d", i, j)
				if j > 0 {
					parent = fmt.Sprintf("snapshot-%d-%d", i, j-1)
				}
				objects = append(objects, newSnapshot(key, parent, false, false))
			}
			objects = append(objects, container(fmt.Sprintf("container-%d", i), fmt.Sprintf("snapshot-%d-%d", i, lastSnapshot)))

		}

		// TODO: Create set of objects for removal

		var (
			ctx = context.Background()

			remaining []gc.Node
		)

		if err := mdb.Update(func(tx *bolt.Tx) error {
			for _, obj := range objects {
				node, err := create(obj, tx, mdb, cs, sn)
				if err != nil {
					return err
				}
				if node != nil {
					remaining = append(remaining, *node)
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("Creation failed: %+v", err)
		}

		// TODO: reset benchmark
		b.ResetTimer()
		//b.StopTimer()

		labels := pprof.Labels("worker", "trigger")
		pprof.Do(ctx, labels, func(ctx context.Context) {
			for i := 0; i < b.N; i++ {

				// TODO: Add removal objects

				//b.StartTimer()

				if _, err := mdb.GarbageCollect(ctx); err != nil {
					b.Fatal(err)
				}

				//b.StopTimer()

				//var actual []gc.Node

				//if err := db.View(func(tx *bolt.Tx) error {
				//	nodeC := make(chan gc.Node)
				//	var scanErr error
				//	go func() {
				//		defer close(nodeC)
				//		scanErr = scanAll(ctx, tx, nodeC)
				//	}()
				//	for node := range nodeC {
				//		actual = append(actual, node)
				//	}
				//	return scanErr
				//}); err != nil {
				//	t.Fatal(err)
				//}

				//checkNodesEqual(t, actual, remaining)
			}
		})
	}
}

func bytesFor(i int64) []byte {
	r := rand.New(rand.NewSource(i))
	var b [256]byte
	_, err := r.Read(b[:])
	if err != nil {
		panic(err)
	}
	return b[:]
}

func digestFor(i int64) digest.Digest {
	r := rand.New(rand.NewSource(i))
	dgstr := digest.SHA256.Digester()
	_, err := io.Copy(dgstr.Hash(), io.LimitReader(r, 256))
	if err != nil {
		panic(err)
	}
	return dgstr.Digest()
}

type object struct {
	data    interface{}
	removed bool
	labels  map[string]string
}

func create(obj object, tx *bolt.Tx, db *DB, cs content.Store, sn snapshots.Snapshotter) (*gc.Node, error) {
	var (
		node      *gc.Node
		namespace = "test"
		ctx       = WithTransactionContext(namespaces.WithNamespace(context.Background(), namespace), tx)
	)

	switch v := obj.data.(type) {
	case testContent:
		expected := digest.FromBytes(v.data)
		w, err := cs.Writer(ctx,
			content.WithRef("test-ref"),
			content.WithDescriptor(ocispec.Descriptor{Size: int64(len(v.data)), Digest: expected}))
		if err != nil {
			return nil, errors.Wrap(err, "failed to create writer")
		}
		if _, err := w.Write(v.data); err != nil {
			return nil, errors.Wrap(err, "write blob failed")
		}
		if err := w.Commit(ctx, int64(len(v.data)), expected, content.WithLabels(obj.labels)); err != nil {
			return nil, errors.Wrap(err, "failed to commit blob")
		}
		if !obj.removed {
			node = &gc.Node{
				Type:      ResourceContent,
				Namespace: namespace,
				Key:       expected.String(),
			}
		}
	case testSnapshot:
		if v.active {
			_, err := sn.Prepare(ctx, v.key, v.parent, snapshots.WithLabels(obj.labels))
			if err != nil {
				return nil, err
			}
		} else {
			akey := fmt.Sprintf("%s-active", v.key)
			_, err := sn.Prepare(ctx, akey, v.parent)
			if err != nil {
				return nil, err
			}
			if err := sn.Commit(ctx, v.key, akey, snapshots.WithLabels(obj.labels)); err != nil {
				return nil, err
			}
		}
		if !obj.removed {
			node = &gc.Node{
				Type:      ResourceSnapshot,
				Namespace: namespace,
				Key:       fmt.Sprintf("native/%s", v.key),
			}
		}
	case testImage:
		image := images.Image{
			Name:   v.name,
			Target: v.target,
			Labels: obj.labels,
		}

		_, err := NewImageStore(db).Create(ctx, image)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create image")
		}
	case testContainer:
		container := containers.Container{
			ID:          v.id,
			SnapshotKey: v.snapshot,
			Snapshotter: "native",
			Labels:      obj.labels,

			Runtime: containers.RuntimeInfo{
				Name: "testruntime",
			},
			Spec: &types.Any{},
		}
		_, err := NewContainerStore(db).Create(ctx, container)
		if err != nil {
			return nil, err
		}
	case testLease:
		lm := NewLeaseManager(db)

		l, err := lm.Create(ctx, leases.WithID(v.id), leases.WithLabels(obj.labels))
		if err != nil {
			return nil, err
		}

		for _, ref := range v.refs {
			if err := lm.AddResource(ctx, l, ref); err != nil {
				return nil, err
			}
		}

		if !obj.removed {
			node = &gc.Node{
				Type:      ResourceLease,
				Namespace: namespace,
				Key:       v.id,
			}
		}
	}

	return node, nil
}

func blob(b []byte, r bool, l ...string) object {
	return object{
		data: testContent{
			data: b,
		},
		removed: r,
		labels:  labelmap(l...),
	}
}

func image(n string, d digest.Digest, l ...string) object {
	return object{
		data: testImage{
			name: n,
			target: ocispec.Descriptor{
				MediaType: "irrelevant",
				Digest:    d,
				Size:      256,
			},
		},
		removed: false,
		labels:  labelmap(l...),
	}
}

func newSnapshot(key, parent string, active, r bool, l ...string) object {
	return object{
		data: testSnapshot{
			key:    key,
			parent: parent,
			active: active,
		},
		removed: r,
		labels:  labelmap(l...),
	}
}

func container(id, s string, l ...string) object {
	return object{
		data: testContainer{
			id:       id,
			snapshot: s,
		},
		removed: false,
		labels:  labelmap(l...),
	}
}

func lease(id string, refs []leases.Resource, r bool, l ...string) object {
	return object{
		data: testLease{
			id:   id,
			refs: refs,
		},
		removed: r,
		labels:  labelmap(l...),
	}
}

type testContent struct {
	data []byte
}

type testSnapshot struct {
	key    string
	parent string
	active bool
}

type testImage struct {
	name   string
	target ocispec.Descriptor
}

type testContainer struct {
	id       string
	snapshot string
}

type testLease struct {
	id   string
	refs []leases.Resource
}

func newStores(t testing.TB) (*DB, content.Store, snapshots.Snapshotter, func()) {
	td, err := ioutil.TempDir("", "gc-test-")
	if err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(filepath.Join(td, "meta.db"), 0644, nil)
	if err != nil {
		t.Fatal(err)
	}

	nsn, err := native.NewSnapshotter(filepath.Join(td, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}

	lcs, err := local.NewStore(filepath.Join(td, "content"))
	if err != nil {
		t.Fatal(err)
	}

	mdb := NewDB(db, lcs, map[string]snapshots.Snapshotter{"native": nsn})

	return mdb, mdb.ContentStore(), mdb.Snapshotter("native"), func() {
		os.RemoveAll(td)
	}
}
